// Command object-store-bootstrap 是 V9.2 Phase 4 (P4.7) 的 Object Store 初始化 Job。
//
// 它只在部署侧 bootstrap 阶段运行，用 S3-compatible endpoint（冻结 SoT：MinIO 或
// 正式 Erratum 批准的 S3-compatible 存储）create_or_validate 两个 bucket：
//
//	aiops-evidence   large Evidence objects
//	aiops-knowledge  Knowledge objects
//
// 幂等：bucket 已存在 → OK（不重建）。runtime 不负责 CreateBucket。
// Object key 契约：<tenant_id>/<cluster_id>/<run_id>/<evidence_id>（evidence）、
// <tenant_id>/<cluster_id>/<doc_id>（knowledge），强制 tenant_id。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const evidenceBucket = "aiops-evidence"
const knowledgeBucket = "aiops-knowledge"

func main() {
	var endpoint, accessKey, secretKey, bucketToCheck string
	var useSSL bool
	flag.StringVar(&endpoint, "endpoint", envOr("S3_ENDPOINT", "127.0.0.1:19000"), "S3-compatible endpoint host:port")
	flag.StringVar(&accessKey, "access-key", envOr("S3_ACCESS_KEY", "minioadmin"), "S3 access key")
	flag.StringVar(&secretKey, "secret-key", envOr("S3_SECRET_KEY", "minioadmin"), "S3 secret key")
	flag.StringVar(&bucketToCheck, "check-bucket", "", "if set, only validate this bucket exists (readiness); exit 1 if missing")
	flag.BoolVar(&useSSL, "ssl", false, "use TLS")
	flag.Parse()

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		fatalf("init client: %v", err)
	}
	ctx := context.Background()

	if bucketToCheck != "" {
		// readiness：只校验 bucket 存在。
		exists, err := client.BucketExists(ctx, bucketToCheck)
		if err != nil {
			fatalf("check bucket %s: %v", bucketToCheck, err)
		}
		if !exists {
			fmt.Fprintf(os.Stderr, "object-store-bootstrap: bucket %s missing (run bootstrap)\n", bucketToCheck)
			os.Exit(1)
		}
		fmt.Printf("CHECK_OK bucket=%s\n", bucketToCheck)
		return
	}

	// bootstrap：create_or_validate 两个 bucket（幂等）。
	for _, name := range []string{evidenceBucket, knowledgeBucket} {
		if err := createOrValidate(client, ctx, name); err != nil {
			fatalf("bootstrap %s: %v", name, err)
		}
	}
	fmt.Println("object-store-bootstrap: buckets aiops-evidence / aiops-knowledge ready (idempotent)")
}

func createOrValidate(client *minio.Client, ctx context.Context, name string) error {
	exists, err := client.BucketExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		fmt.Printf("BUCKET_EXISTS %s (skip)\n", name)
		return nil
	}
	if err := client.MakeBucket(ctx, name, minio.MakeBucketOptions{}); err != nil {
		return err
	}
	fmt.Printf("BUCKET_CREATED %s\n", name)
	// 校验创建成功。
	ok, err := client.BucketExists(ctx, name)
	if err != nil || !ok {
		return fmt.Errorf("bucket %s not verifiable after create", name)
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "object-store-bootstrap: "+format+"\n", args...)
	os.Exit(1)
}
