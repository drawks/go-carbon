package logging

import (
	"bytes"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/howeyc/fsnotify"
)

var std = NewFileLogger()

func StandardLogger() *FileLogger {
	return std
}

type Fields logrus.Fields

// FileLogger wrapper
type FileLogger struct {
	sync.RWMutex
	filename    string
	fd          *os.File
	watcherDone chan bool
	*logrus.Logger
}

// NewFileLogger create instance FileLogger
func NewFileLogger() *FileLogger {
	logrus.SetFormatter(&TextFormatter{})
	return &FileLogger{
		filename:    "",
		fd:          nil,
		watcherDone: nil,
		Logger:      logrus.StandardLogger(),
	}
}

// Open file for logging
func (l *FileLogger) Open(filename string) error {
	l.Lock()
	l.filename = filename
	l.Unlock()

	reopenErr := l.Reopen()
	if l.watcherDone != nil {
		close(l.watcherDone)
	}
	l.watcherDone = make(chan bool)
	l.fsWatch(l.filename, l.watcherDone)

	return reopenErr
}

//
func (l *FileLogger) fsWatch(filename string, quit chan bool) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logrus.Warningf("fsnotify.NewWatcher(): %s", err)
		return
	}

	if filename == "" {
		return
	}

	subscribe := func() {
		if err := watcher.WatchFlags(filename, fsnotify.FSN_CREATE|fsnotify.FSN_DELETE|fsnotify.FSN_RENAME); err != nil {
			logrus.Warningf("fsnotify.Watcher.Watch(%s): %s", filename, err)
		}
	}

	subscribe()

	go func() {
		defer watcher.Close()

		for {
			select {
			case <-watcher.Event:
				l.Reopen()
				subscribe()

				logrus.Infof("Reopen log %#v by fsnotify event", std.Filename())
				if err != nil {
					logrus.Errorf("Reopen log %#v failed: %s", std.Filename(), err.Error())
				}

			case <-quit:
				return
			}
		}
	}()
}

// Reopen file
func (l *FileLogger) Reopen() error {
	l.Lock()
	defer l.Unlock()

	var newFd *os.File
	var err error

	if l.filename != "" {
		newFd, err = os.OpenFile(l.filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)

		if err != nil {
			return err
		}
	} else {
		newFd = nil
	}

	oldFd := l.fd
	l.fd = newFd

	var loggerOut io.Writer

	if l.fd != nil {
		loggerOut = l.fd
	} else {
		loggerOut = os.Stderr
	}
	logrus.SetOutput(loggerOut)

	if oldFd != nil {
		oldFd.Close()
	}

	return nil
}

// Filename returns current filename
func (l *FileLogger) Filename() string {
	l.RLock()
	defer l.RUnlock()
	return l.filename
}

// SetFile for default logger
func SetFile(filename string) error {
	return std.Open(filename)
}

// SetLevel for default logger
func SetLevel(lvl string) error {
	level, err := logrus.ParseLevel(lvl)
	if err != nil {
		return err
	}
	logrus.SetLevel(level)
	return nil
}

// PrepareFile creates logfile and set it writable for user
func PrepareFile(filename string, owner *user.User) error {
	if filename == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}

	fd, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if fd != nil {
		fd.Close()
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(filename, 0644); err != nil {
		return err
	}
	if owner != nil {

		uid, err := strconv.ParseInt(owner.Uid, 10, 0)
		if err != nil {
			return err
		}

		gid, err := strconv.ParseInt(owner.Gid, 10, 0)
		if err != nil {
			return err
		}

		if err := os.Chown(filename, int(uid), int(gid)); err != nil {
			return err
		}
	}

	return nil
}

type TestOut interface {
	Write(p []byte) (n int, err error)
	String() string
}

type buffer struct {
	sync.Mutex
	b bytes.Buffer
}

func (b *buffer) Write(p []byte) (n int, err error) {
	b.Lock()
	defer b.Unlock()
	return b.b.Write(p)
}
func (b *buffer) String() string {
	b.Lock()
	defer b.Unlock()
	return b.b.String()
}

// Test run callable with changed logging output
func Test(callable func(TestOut)) {
	buf := &buffer{}
	logrus.SetOutput(buf)

	callable(buf)

	var loggerOut io.Writer
	if std.fd != nil {
		loggerOut = std.fd
	} else {
		loggerOut = os.Stderr
	}

	logrus.SetOutput(loggerOut)
}

// TestWithLevel run callable with changed logging output and log level
func TestWithLevel(level string, callable func(TestOut)) {
	originalLevel := logrus.GetLevel()
	defer logrus.SetLevel(originalLevel)
	SetLevel(level)

	Test(callable)
}

// WithError creates an entry from the standard logger and adds an error to it, using the value defined in ErrorKey as key.
func WithError(err error) *logrus.Entry {
	return std.WithField("error", err)
}

// WithField creates an entry from the standard logger and adds a field to
// it. If you want multiple fields, use `WithFields`.
//
// Note that it doesn't log until you call Debug, Print, Info, Warn, Fatal
// or Panic on the Entry it returns.
func WithField(key string, value interface{}) *logrus.Entry {
	return std.WithField(key, value)
}

// WithFields creates an entry from the standard logger and adds multiple
// fields to it. This is simply a helper for `WithField`, invoking it
// once for each field.
//
// Note that it doesn't log until you call Debug, Print, Info, Warn, Fatal
// or Panic on the Entry it returns.
func WithFields(fields Fields) *logrus.Entry {
	return std.WithFields(logrus.Fields(fields))
}

// Debug logs a message at level Debug on the standard logger.
func Debug(args ...interface{}) {
	std.Debug(args...)
}

// Print logs a message at level Info on the standard logger.
func Print(args ...interface{}) {
	std.Print(args...)
}

// Info logs a message at level Info on the standard logger.
func Info(args ...interface{}) {
	std.Info(args...)
}

// Warn logs a message at level Warn on the standard logger.
func Warn(args ...interface{}) {
	std.Warn(args...)
}

// Warning logs a message at level Warn on the standard logger.
func Warning(args ...interface{}) {
	std.Warning(args...)
}

// Error logs a message at level Error on the standard logger.
func Error(args ...interface{}) {
	std.Error(args...)
}

// Panic logs a message at level Panic on the standard logger.
func Panic(args ...interface{}) {
	std.Panic(args...)
}

// Fatal logs a message at level Fatal on the standard logger.
func Fatal(args ...interface{}) {
	std.Fatal(args...)
}

// Debugf logs a message at level Debug on the standard logger.
func Debugf(format string, args ...interface{}) {
	std.Debugf(format, args...)
}

// Printf logs a message at level Info on the standard logger.
func Printf(format string, args ...interface{}) {
	std.Printf(format, args...)
}

// Infof logs a message at level Info on the standard logger.
func Infof(format string, args ...interface{}) {
	std.Infof(format, args...)
}

// Warnf logs a message at level Warn on the standard logger.
func Warnf(format string, args ...interface{}) {
	std.Warnf(format, args...)
}

// Warningf logs a message at level Warn on the standard logger.
func Warningf(format string, args ...interface{}) {
	std.Warningf(format, args...)
}

// Errorf logs a message at level Error on the standard logger.
func Errorf(format string, args ...interface{}) {
	std.Errorf(format, args...)
}

// Panicf logs a message at level Panic on the standard logger.
func Panicf(format string, args ...interface{}) {
	std.Panicf(format, args...)
}

// Fatalf logs a message at level Fatal on the standard logger.
func Fatalf(format string, args ...interface{}) {
	std.Fatalf(format, args...)
}

// Debugln logs a message at level Debug on the standard logger.
func Debugln(args ...interface{}) {
	std.Debugln(args...)
}

// Println logs a message at level Info on the standard logger.
func Println(args ...interface{}) {
	std.Println(args...)
}

// Infoln logs a message at level Info on the standard logger.
func Infoln(args ...interface{}) {
	std.Infoln(args...)
}

// Warnln logs a message at level Warn on the standard logger.
func Warnln(args ...interface{}) {
	std.Warnln(args...)
}

// Warningln logs a message at level Warn on the standard logger.
func Warningln(args ...interface{}) {
	std.Warningln(args...)
}

// Errorln logs a message at level Error on the standard logger.
func Errorln(args ...interface{}) {
	std.Errorln(args...)
}

// Panicln logs a message at level Panic on the standard logger.
func Panicln(args ...interface{}) {
	std.Panicln(args...)
}

// Fatalln logs a message at level Fatal on the standard logger.
func Fatalln(args ...interface{}) {
	std.Fatalln(args...)
}
