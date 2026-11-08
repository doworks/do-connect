package calling

import (
	"fmt"

	"github.com/pion/logging"
	"github.com/zerodha/logf"
)

// pionLogs passes the WebRTC library's warnings and errors to our log, so a
// call whose audio drops says why (a DTLS alert, an ICE timeout) instead of
// just "closed". Its chattier levels are dropped.
type pionLogs struct{ log logf.Logger }

func (p pionLogs) NewLogger(scope string) logging.LeveledLogger {
	return pionLogger{log: p.log, scope: scope}
}

type pionLogger struct {
	log   logf.Logger
	scope string
}

func (pionLogger) Trace(string)          {}
func (pionLogger) Tracef(string, ...any) {}
func (pionLogger) Debug(string)          {}
func (pionLogger) Debugf(string, ...any) {}
func (pionLogger) Info(string)           {}
func (pionLogger) Infof(string, ...any)  {}
func (l pionLogger) Warn(msg string)     { l.log.Warn("WebRTC: "+msg, "scope", l.scope) }
func (l pionLogger) Warnf(format string, args ...any) {
	l.log.Warn("WebRTC: "+fmt.Sprintf(format, args...), "scope", l.scope)
}
func (l pionLogger) Error(msg string) { l.log.Error("WebRTC: "+msg, "scope", l.scope) }
func (l pionLogger) Errorf(format string, args ...any) {
	l.log.Error("WebRTC: "+fmt.Sprintf(format, args...), "scope", l.scope)
}
