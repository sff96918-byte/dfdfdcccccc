package bot

import "fmt"

type LogFunc func(format string, args ...any)

func logf(cfg Config, format string, args ...any) {
	if cfg.Logf != nil {
		cfg.Logf(format, args...)
		return
	}
	fmt.Printf(format, args...)
}

func logln(cfg Config, message string) {
	logf(cfg, "%s\n", message)
}

func activityf(cfg Config, format string, args ...any) {
	if cfg.Activityf != nil {
		cfg.Activityf(format, args...)
		return
	}
	logf(cfg, format, args...)
}
