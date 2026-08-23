package cutover

import (
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// P6.4.4 Atomic Cutover Coordinator
//
// 把已冻结的 rollback/abort 状态机接到生产 writer/reader 的真实激活/停用动作上。
//   - ActivateNewWriter / ActivateNewReader / StopOldWriter / StopOldReader 是唯一受控入口
//   - StopOld* 只能经 coordinator（内部经 State 铁律：fresh-data PASS 前禁 stop old），无旁路
//   - 提供可观察状态（cutover_id/current_state/transitions/flags/failure/abort_reason）
// ─────────────────────────────────────────────────────────────────────────────

// RuntimeWriter 生产 writer 的受控控制面（真实接线：new VM·VLogs writer + legacy ClickHouse writer）。
//
// 语义澄清（P6.5 semantic sanity）：StopOldWriter 停止的是【legacy writer】（独立控制面
// StopLegacy），绝不能通过把 new writer 设回 disabled 来实现。new writer 在 cutover 后
// 保持 ACTIVE 继续写 VM/VLogs。
type RuntimeWriter interface {
	// ActivateNew 启用 new writer（真实进入生产写 VM/VLogs）。返回真实生效 new 状态。
	ActivateNew() bool
	// IsWritingNew 返回当前是否实际写 new backend（用于 reconcile runtime 与 state）。
	IsWritingNew() bool
	// StopLegacy 停止 legacy writer（ClickHouse 写路径）。new writer 不受影响（保持 ACTIVE）。
	StopLegacy()
}

// RuntimeReader 生产 reader 的受控控制面（真实接线：query-api reader 路由）。
type RuntimeReader interface {
	// ActivateNew 启用 new reader（metrics→VM、raw logs→VLogs）。
	ActivateNew() bool
	// IsReadingNew 返回当前是否实际读 new backend。
	IsReadingNew() bool
	// StopLegacy 停止 legacy reader（ClickHouse 查询路径）。new reader 不受影响。
	StopLegacy()
}

// WriterMode / ReaderMode 常量（与 runtime 层约定）。
const (
	ModeDisabled = 0
	ModeNew      = 1
)

// Coordinator 是原子 cutover 的受控执行器。
type Coordinator struct {
	mu   sync.Mutex
	state *State

	writer RuntimeWriter
	reader RuntimeReader

	// 可观察状态。
	id              string
	startedAt       time.Time
	lastTransition  time.Time
	failureCheckpoint Phase
	abortReason     string
}

// NewCoordinator 构造 coordinator，注入生产 writer/reader 控制面。
func NewCoordinator(id string, writer RuntimeWriter, reader RuntimeReader) *Coordinator {
	now := time.Now().UTC()
	return &Coordinator{
		state:     New(),
		writer:    writer,
		reader:    reader,
		id:        id,
		startedAt: now,
		lastTransition: now,
	}
}

// errNoWriter/errNoReader 用于未注入控制面时的 fail-closed。
func (c *Coordinator) recordTransition() { c.lastTransition = time.Now().UTC() }

// ActivateNewWriter 激活 new writer（唯一受控入口）。要求当前处于激活新 writer 阶段。
func (c *Coordinator) ActivateNewWriter() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Phase != PhasePrecheck && c.state.Phase != PhaseActivateNewWriter {
		return &Error{Checkpoint: c.state.Phase, Reason: "ActivateNewWriter only allowed at precheck/activate-new-writer"}
	}
	if c.writer == nil {
		return &Error{Checkpoint: c.state.Phase, Reason: "writer runtime control not injected"}
	}
	if !c.writer.ActivateNew() {
		return &Error{Checkpoint: c.state.Phase, Reason: "writer did not enter new mode"}
	}
	if !c.writer.IsWritingNew() {
		return &Error{Checkpoint: c.state.Phase, Reason: "writer not actually writing new backend"}
	}
	if err := c.state.advance(); err != nil {
		return &Error{Checkpoint: c.state.Phase, Reason: err.Error()}
	}
	c.recordTransition()
	return nil
}

// ActivateNewReader 激活 new reader（唯一受控入口）。
func (c *Coordinator) ActivateNewReader() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Phase != PhaseActivateNewWriter {
		return &Error{Checkpoint: c.state.Phase, Reason: "ActivateNewReader only allowed after new writer active"}
	}
	if c.reader == nil {
		return &Error{Checkpoint: c.state.Phase, Reason: "reader runtime control not injected"}
	}
	if !c.reader.ActivateNew() {
		return &Error{Checkpoint: c.state.Phase, Reason: "reader did not enter new mode"}
	}
	if !c.reader.IsReadingNew() {
		return &Error{Checkpoint: c.state.Phase, Reason: "reader not actually reading new backend"}
	}
	if err := c.state.advance(); err != nil {
		return &Error{Checkpoint: c.state.Phase, Reason: err.Error()}
	}
	c.recordTransition()
	return nil
}

// VerifyFreshData 真实 fresh-data 校验（驱动状态迁移；铁律：PASS 前禁 stop old）。
// 由调用方提供真实 telemetry 校验（tenant/cluster/resource/timestamp），返回 bool。
// PASS 后推进通过 scope/semantic 校验 checkpoint，到达 STOP_OLD_WRITER 前（old 可停）。
func (c *Coordinator) VerifyFreshData(verified bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !verified {
		// fresh data 不可见 → ABORT（不停 old）。
		c.failureCheckpoint = c.state.Phase
		c.abortReason = "fresh data not verified"
		return &Error{Checkpoint: c.state.Phase, Reason: "fresh data not visible; ABORT, old path unchanged", Decision: DecisionAbort}
	}
	c.state.MarkFreshDataVerified()
	// 推进通过 FRESH_DATA / SCOPE / SEMANTIC 三个 verify checkpoint，到达 STOP_OLD_WRITER。
	for c.state.Phase < PhaseStopOldWriter {
		if err := c.state.advance(); err != nil {
			return &Error{Checkpoint: c.state.Phase, Reason: err.Error()}
		}
	}
	c.recordTransition()
	return nil
}

// StopOldWriter 停止 old writer（唯一受控入口；内部经 State 铁律，无旁路）。
func (c *Coordinator) StopOldWriter() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Phase != PhaseStopOldWriter {
		return &Error{Checkpoint: c.state.Phase, Reason: "StopOldWriter only allowed at stop-old-writer phase"}
	}
	if !c.state.FreshDataVerified {
		// 理论上 State.advance 已拦；此处双保险，确保无旁路。
		return &Error{Checkpoint: c.state.Phase, Reason: "cannot stop old writer before fresh-data verified", Decision: DecisionAbort}
	}
	// 实际停用：只停 legacy writer（独立控制面），new writer 保持 ACTIVE 继续写 VM/VLogs。
	if c.writer != nil {
		c.writer.StopLegacy()
	}
	c.state.StopOldWriter()
	if err := c.state.advance(); err != nil {
		return &Error{Checkpoint: c.state.Phase, Reason: err.Error()}
	}
	c.recordTransition()
	return nil
}

// StopOldReader 停止 old reader（唯一受控入口；无旁路）。
func (c *Coordinator) StopOldReader() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Phase != PhaseStopOldReader {
		return &Error{Checkpoint: c.state.Phase, Reason: "StopOldReader only allowed at stop-old-reader phase"}
	}
	if !c.state.FreshDataVerified {
		return &Error{Checkpoint: c.state.Phase, Reason: "cannot stop old reader before fresh-data verified", Decision: DecisionAbort}
	}
	// 只停 legacy reader，new reader 保持 ACTIVE。
	if c.reader != nil {
		c.reader.StopLegacy()
	}
	c.state.StopOldReader()
	if err := c.state.advance(); err != nil {
		return &Error{Checkpoint: c.state.Phase, Reason: err.Error()}
	}
	c.recordTransition()
	return nil
}

// Fail 报告 checkpoint 失败，返回处置决策。
func (c *Coordinator) Fail(checkpoint Phase, reason string) Decision {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failureCheckpoint = checkpoint
	c.abortReason = reason
	return c.state.Fail(checkpoint)
}

// Status 输出可观察 cutover 状态。
func (c *Coordinator) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{
		CutoverID:          c.id,
		CurrentState:       c.state.Phase.String(),
		StartedAt:          c.startedAt,
		LastTransitionAt:   c.lastTransition,
		NewWriterActive:    c.state.ActiveWriter,
		NewReaderActive:    c.state.ActiveReader,
		FreshDataVerified:  c.state.FreshDataVerified,
		OldWriterStopped:   c.state.OldWriterStopped,
		OldReaderStopped:   c.state.OldReaderStopped,
		FailureCheckpoint:  c.failureCheckpoint.String(),
		AbortReason:        c.abortReason,
	}
}

// Status 是 coordinator 的可观察快照。
type Status struct {
	CutoverID        string    `json:"cutover_id"`
	CurrentState     string    `json:"current_state"`
	StartedAt        time.Time `json:"started_at"`
	LastTransitionAt time.Time `json:"last_transition_at"`
	NewWriterActive  bool      `json:"new_writer_active"`
	NewReaderActive  bool      `json:"new_reader_active"`
	FreshDataVerified bool     `json:"fresh_data_verified"`
	OldWriterStopped bool      `json:"old_writer_stopped"`
	OldReaderStopped bool      `json:"old_reader_stopped"`
	FailureCheckpoint string   `json:"failure_checkpoint"`
	AbortReason      string    `json:"abort_reason"`
}

// Error 是 coordinator 错误，携带 checkpoint/decision。
type Error struct {
	Checkpoint Phase
	Reason     string
	Decision   Decision
}

func (e *Error) Error() string { return e.Reason }
