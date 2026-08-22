package liveattestation

import (
	"context"
	"errors"
)

var (
	// ErrUnsupportedPlatform 表示当前 OS/架构不能生成 ChatGPT DeviceCheck。
	ErrUnsupportedPlatform = errors.New("live DeviceCheck attestation is only available on Apple Silicon with the official ChatGPT app")
	// ErrChatGPTAppMissing 表示宿主机没有可用的官方 ChatGPT.app。
	ErrChatGPTAppMissing = errors.New("live attestation requires the official ChatGPT app on this host")
)

// Provider 在发起 Live 请求前生成 ChatGPT DeviceCheck attestation。
type Provider interface {
	Check(ctx context.Context) error
	Generate(ctx context.Context) (string, error)
}
