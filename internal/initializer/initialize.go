package initializer

import (
	"time"
)

type Result struct {
	startTime   time.Time
	endTime     time.Time
	elapsedTime time.Duration

	filesWritten []string
}

func Initialize(version string, pkg string, fileFormat FileFormat) error {
	renderFiles(version, pkg, fileFormat)
	return nil
}
