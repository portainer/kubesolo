//go:build !linux

package assistant

import "context"

type diskLayout struct{ dataDir, podLogsDir, modelFile string }

func diskUsage(context.Context, diskLayout) map[string]int64 { return nil }
