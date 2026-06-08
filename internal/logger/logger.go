package logger

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

func (l LogLevel) Icon() string {
	switch l {
	case DEBUG:
		return "🔍"
	case INFO:
		return "ℹ️"
	case WARN:
		return "⚠️"
	case ERROR:
		return "❌"
	default:
		return "📝"
	}
}

// LogEntry represents a single log entry
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     LogLevel  `json:"level"`
	Message   string    `json:"message"`
	Icon      string    `json:"icon"`
	LevelStr  string    `json:"levelStr"`
}

// Logger manages application logs
type Logger struct {
	mu           sync.RWMutex
	entries      []LogEntry
	maxSize      int
	minLevel     LogLevel // Minimum level to record
	consoleLevel LogLevel // Minimum level to print to console
	debugFile    *os.File // Debug log file (only in debug mode)
	debugMu      sync.Mutex
	subsMu       sync.RWMutex
	subs         map[int]chan LogEntry // SSE subscribers (real-time tail)
	nextSubID    int
}

var (
	instance *Logger
	once     sync.Once
)

// GetLogger returns the singleton logger instance
func GetLogger() *Logger {
	once.Do(func() {
		instance = &Logger{
			entries:      make([]LogEntry, 0),
			maxSize:      1000,  // Keep last 1000 logs
			minLevel:     DEBUG, // Default to DEBUG level to capture all logs
			consoleLevel: INFO,  // Default console level to INFO (skip DEBUG in console)
			subs:         make(map[int]chan LogEntry),
		}
	})
	return instance
}

// SetMinLevel sets the minimum log level to record
func (l *Logger) SetMinLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.minLevel = level
}

// SetConsoleLevel sets the minimum log level to print to console
func (l *Logger) SetConsoleLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consoleLevel = level
}

// GetMinLevel returns the current minimum log level
func (l *Logger) GetMinLevel() LogLevel {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.minLevel
}

// Log adds a new log entry
func (l *Logger) Log(level LogLevel, format string, args ...interface{}) {
	// Hot-path early-out: drop *before* formatting. fmt.Sprintf on every
	// silenced SSE event would allocate many MB across a long stream.
	l.mu.RLock()
	minLevel := l.minLevel
	l.mu.RUnlock()
	if level < minLevel {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Re-check under the write lock — minLevel might have moved between the
	// RLock check and us acquiring the write lock.
	if level < l.minLevel {
		return
	}

	message := fmt.Sprintf(format, args...)
	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		Icon:      level.Icon(),
		LevelStr:  level.String(),
	}

	// Add to memory
	l.entries = append(l.entries, entry)

	// Trim if exceeds max size
	if len(l.entries) > l.maxSize {
		l.entries = l.entries[len(l.entries)-l.maxSize:]
	}

	// Print to console only if level >= consoleLevel
	if level >= l.consoleLevel {
		fmt.Printf("%s [%s] %s\n", entry.Icon, entry.LevelStr, entry.Message)
	}

	// Fan-out to SSE subscribers. Non-blocking: a slow subscriber misses
	// entries rather than holding up the logger lock. We snapshot the
	// subscriber list under subsMu so iteration doesn't race Subscribe.
	l.subsMu.RLock()
	for _, ch := range l.subs {
		select {
		case ch <- entry:
		default:
			// drop — subscriber is too slow
		}
	}
	l.subsMu.RUnlock()
}

// Subscribe registers a buffered channel that receives every new log entry
// recorded at or above minLevel. Returns the subscription ID and the channel.
// Caller MUST drain the channel and call Unsubscribe when done — otherwise
// the buffer fills and entries are silently dropped (which is benign but
// shows as gaps in the UI).
func (l *Logger) Subscribe(buffer int) (int, <-chan LogEntry) {
	if buffer < 1 {
		buffer = 64
	}
	ch := make(chan LogEntry, buffer)
	l.subsMu.Lock()
	defer l.subsMu.Unlock()
	l.nextSubID++
	id := l.nextSubID
	l.subs[id] = ch
	return id, ch
}

// Unsubscribe removes a subscription and closes its channel.
func (l *Logger) Unsubscribe(id int) {
	l.subsMu.Lock()
	defer l.subsMu.Unlock()
	if ch, ok := l.subs[id]; ok {
		delete(l.subs, id)
		close(ch)
	}
}

// GetLogs returns all log entries
func (l *Logger) GetLogs() []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Return a copy
	result := make([]LogEntry, len(l.entries))
	copy(result, l.entries)
	return result
}

// GetLogsByLevel returns logs filtered by minimum level
func (l *Logger) GetLogsByLevel(minLevel LogLevel) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]LogEntry, 0)
	for _, entry := range l.entries {
		if entry.Level >= minLevel {
			result = append(result, entry)
		}
	}
	return result
}

// Clear removes all log entries
func (l *Logger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = make([]LogEntry, 0)
}

// Convenience methods
func Debug(format string, args ...interface{}) {
	GetLogger().Log(DEBUG, format, args...)
}

func Info(format string, args ...interface{}) {
	GetLogger().Log(INFO, format, args...)
}

func Warn(format string, args ...interface{}) {
	GetLogger().Log(WARN, format, args...)
}

func Error(format string, args ...interface{}) {
	GetLogger().Log(ERROR, format, args...)
}

// EnableDebugFile enables debug file logging (only in debug mode)
func (l *Logger) EnableDebugFile(filepath string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.debugFile != nil {
		l.debugFile.Close()
	}

	f, err := os.OpenFile(filepath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	l.debugFile = f
	return nil
}

// DebugLog writes to debug.log file (bypasses log level)
func (l *Logger) DebugLog(format string, args ...interface{}) {
	l.debugMu.Lock()
	defer l.debugMu.Unlock()

	if l.debugFile == nil {
		return
	}

	message := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(l.debugFile, "[%s] %s\n", timestamp, message)
}

// Close closes the debug log file
func (l *Logger) Close() {
	l.debugMu.Lock()
	if l.debugFile != nil {
		l.debugFile.Close()
		l.debugFile = nil
	}
	l.debugMu.Unlock()
}

// DebugLog writes to debug.log file (convenience function)
func DebugLog(format string, args ...interface{}) {
	GetLogger().DebugLog(format, args...)
}
