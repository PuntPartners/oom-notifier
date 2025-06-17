package logger

import (
	"log"
	"os"
)

var (
	debugEnabled bool
	infoLogger   *log.Logger
	debugLogger  *log.Logger
)

func Init(debug bool) {
	debugEnabled = debug

	// Set up loggers to write to stdout
	infoLogger = log.New(os.Stdout, "", log.LstdFlags)
	debugLogger = log.New(os.Stdout, "", log.LstdFlags)
}

func Info(format string, args ...interface{}) {
	infoLogger.Printf("[INFO] "+format, args...)
}

func Error(format string, args ...interface{}) {
	infoLogger.Printf("[ERROR] "+format, args...)
}

func Warn(format string, args ...interface{}) {
	infoLogger.Printf("[WARN] "+format, args...)
}

func Debug(format string, args ...interface{}) {
	if debugEnabled {
		debugLogger.Printf("[DEBUG] "+format, args...)
	}
}
