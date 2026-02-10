package services

import "sync/atomic"

// 全局日志开关（默认关闭，降低能耗/写盘）。
// 约定：关闭后不记录任何日志，包括 request_log、MITM logs、Console logs、RequestDetail 等。
var loggingEnabled atomic.Bool

func UpdateLoggingConfigFromAppSettings(settings AppSettings) {
	loggingEnabled.Store(settings.EnableLogging)
}

func IsLoggingEnabled() bool {
	return loggingEnabled.Load()
}

