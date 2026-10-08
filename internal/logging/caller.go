package logging

import (
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
)

// maxCallerDepth bounds the stack walk for a bridged log line. It is deep
// enough to reach the component that called into a shared library such as
// client-go, not only the library frame that wrote the line.
const maxCallerDepth = 64

// thisPackage is the import path of this package, so its own frames are skipped
// when looking up the caller of a bridged log line.
var thisPackage = func() string {
	pc, _, _, _ := runtime.Caller(0)
	return packagePath(runtime.FuncForPC(pc).Name())
}()

// componentFunc names the kubesolo component that owns a function, or returns
// "" when the function belongs to code shared between components.
type componentFunc func(function string) string

// externalCaller walks the stack past this package and the given logging
// libraries. It returns the frame that wrote the log line, formatted as a
// caller, and the component of the nearest frame from there up that belongs to
// one, so a line written by shared code is attributed to the component that
// called into it.
func externalCaller(component componentFunc, libraries ...string) (caller, owner string) {
	pcs := make([]uintptr, maxCallerDepth)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	for {
		frame, more := frames.Next()
		if caller == "" && !isFrameOf(frame.Function, thisPackage) && !isFrameOf(frame.Function, libraries...) {
			caller = formatCaller(frame)
		}
		if caller != "" {
			if owner = component(frame.Function); owner != "" {
				return caller, owner
			}
		}
		if !more {
			return caller, ""
		}
	}
}

// formatCaller formats a frame as zerolog does, except for code built from the
// module cache, which is named by import path rather than by where the module
// cache happened to be on the build machine.
func formatCaller(frame runtime.Frame) string {
	if strings.Contains(frame.File, "/pkg/mod/") {
		return packagePath(frame.Function) + "/" + filepath.Base(frame.File) + ":" + strconv.Itoa(frame.Line)
	}
	return zerolog.CallerMarshalFunc(frame.PC, frame.File, frame.Line)
}

// isFrameOf reports whether a function, as named by runtime.Frame.Function,
// belongs to one of the given packages or to a package below one of them.
func isFrameOf(function string, packages ...string) bool {
	for _, p := range packages {
		if strings.HasPrefix(function, p) {
			rest := function[len(p):]
			if rest == "" || rest[0] == '.' || rest[0] == '/' {
				return true
			}
		}
	}
	return false
}

// packagePath strips the function and receiver from a function name as returned
// by runtime, leaving the import path of its package.
func packagePath(function string) string {
	slash := strings.LastIndex(function, "/")
	if dot := strings.Index(function[slash+1:], "."); dot >= 0 {
		return function[:slash+1+dot]
	}
	return function
}
