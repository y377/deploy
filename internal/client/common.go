package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// 共享常量
const (
	downloadTimeout      = 30 * time.Second
	minReconnectDelay    = 1 * time.Second  // 最小重连延迟
	maxReconnectDelay    = 30 * time.Second // 最大重连延迟
	fastReconnectAttempt = 3                // 快速重连尝试次数
	heartbeatInterval    = 10 * time.Second // 应用层心跳间隔
)

// 共享变量
var (
	isConnected atomic.Bool
)

// ClientInterface 客户端接口，用于复用业务逻辑
type ClientInterface interface {
	GetClientID() string
	GetAccessKey() string
	GetContext() context.Context
	GetHTTPClient() *http.Client
	downloadFile(downloadURL, filePath string) error
}

// DownloadFile 公共的文件下载函数，可被所有客户端复用
func DownloadFile(ctx context.Context, httpClient *http.Client, accessKey, downloadURL, filePath string) error {
	u, err := url.Parse(downloadURL)
	if err != nil {
		return err
	}

	query := u.Query()
	query.Set("accessKey", accessKey)
	u.RawQuery = query.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", u.String(), nil)
	if err != nil {
		return err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if len(body) > 500 {
			body = body[:500]
		}
		return fmt.Errorf("下载失败，状态码: %d, body: %s", resp.StatusCode, string(body))
	}

	fmt.Printf(
		"证书下载响应 Status=%d Content-Type=%s Content-Length=%s URL=%s\n",
		resp.StatusCode,
		resp.Header.Get("Content-Type"),
		resp.Header.Get("Content-Length"),
		u.String(),
	)

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(filePath), ".anssl-*")
	if err != nil {
		return err
	}

	tmpPath := tmpFile.Name()
	completed := false

	defer func() {
		tmpFile.Close()
		if !completed {
			os.Remove(tmpPath)
		}
	}()

	written, err := io.Copy(tmpFile, resp.Body)
	if err != nil {
		return err
	}

	fmt.Printf("证书下载写入临时文件 bytes=%d tmp=%s target=%s\n", written, tmpPath, filePath)

	if err := tmpFile.Sync(); err != nil {
		return err
	}

	if _, err := tmpFile.Seek(0, 0); err == nil {
		buf := make([]byte, 128)
		n, _ := tmpFile.Read(buf)

		fmt.Printf("证书文件头HEX=%x\n", buf[:n])
		fmt.Printf("证书文件头TEXT=%s\n", string(buf[:n]))
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		return err
	}

	completed = true
	return nil
}
