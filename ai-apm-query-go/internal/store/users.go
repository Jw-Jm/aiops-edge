package store

import (
	"database/sql"
	"errors"
	"time"
)

// User 用户实体。
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	Email        string    `json:"email"`
	Status       int       `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// UserDAO 用户数据访问对象。
type UserDAO struct{}

// List 返回用户列表（分页）。
func (d *UserDAO) List(page, size int) ([]User, int, error) {
	conn := GetDB()
	if conn == nil {
		return nil, 0, errors.New("mysql unavailable")
	}
	if size <= 0 {
		size = 20
	}
	offset := (page - 1) * size
	var total int
	if err := conn.QueryRow("SELECT COUNT(*) FROM users").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := conn.Query(
		"SELECT id, username, password_hash, display_name, role, email, status, created_at FROM users ORDER BY id LIMIT ? OFFSET ?",
		size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName,
			&u.Role, &u.Email, &u.Status, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, nil
}

// GetByUsername 按用户名查用户；未找到返回 nil。
func (d *UserDAO) GetByUsername(username string) (*User, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		"SELECT id, username, password_hash, display_name, role, email, status, created_at FROM users WHERE username = ?",
		username)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName,
		&u.Role, &u.Email, &u.Status, &u.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// GetByID 按 ID 查用户。
func (d *UserDAO) GetByID(id int64) (*User, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		"SELECT id, username, password_hash, display_name, role, email, status, created_at FROM users WHERE id = ?",
		id)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName,
		&u.Role, &u.Email, &u.Status, &u.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// Create 创建用户，返回新 ID。
func (d *UserDAO) Create(u *User) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	res, err := conn.Exec(
		"INSERT INTO users (username, password_hash, display_name, role, email, status) VALUES (?, ?, ?, ?, ?, ?)",
		u.Username, u.PasswordHash, u.DisplayName, u.Role, u.Email, u.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update 更新用户（角色/状态/显示名/邮箱/密码）。fields 为可空更新项。
func (d *UserDAO) Update(id int64, displayName, role, email string, status int, newPasswordHash *string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	var err error
	if newPasswordHash != nil {
		_, err = conn.Exec(
			"UPDATE users SET display_name=?, role=?, email=?, status=?, password_hash=? WHERE id=?",
			displayName, role, email, status, *newPasswordHash, id)
	} else {
		_, err = conn.Exec(
			"UPDATE users SET display_name=?, role=?, email=?, status=? WHERE id=?",
			displayName, role, email, status, id)
	}
	return err
}

// Delete 删除用户。
func (d *UserDAO) Delete(id int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM users WHERE id=?", id)
	return err
}

// SeedAdmin 种子 admin 用户（若不存在）。密码默认 admin123。
func (d *UserDAO) SeedAdmin(hash string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		"INSERT IGNORE INTO users (username, password_hash, display_name, role) VALUES ('admin', ?, '管理员', 'admin')",
		hash)
	return err
}
