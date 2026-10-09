package logging

import (
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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

// formatCaller formats a frame as a caller, by import path.
func formatCaller(frame runtime.Frame) string {
	return callerName(frame.Function, frame.File, frame.Line)
}

// modulePath is the import path of this module.
const modulePath = "github.com/portainer/kubesolo"

// moduleRoot is the directory this module was built from, as the compiler
// recorded it in file names, so it can be replaced by modulePath.
var moduleRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return strings.TrimSuffix(file, "internal/logging/caller.go")
}()

// callerName names a source position by import path, so the same line reads the
// same whichever machine and directory the binary was built in: kubesolo's own
// files under modulePath, and code from the module cache by its package's import
// path. Anything else, such as the standard library, keeps its file name.
func callerName(function, file string, line int) string {
	pos := ":" + strconv.Itoa(line)
	switch {
	case strings.HasPrefix(file, moduleRoot):
		return modulePath + "/" + strings.TrimPrefix(file, moduleRoot) + pos
	case strings.Contains(file, "/pkg/mod/") && function != "":
		return packagePath(function) + "/" + filepath.Base(file) + pos
	}
	return file + pos
}

// callerMarshal is zerolog's CallerMarshalFunc for the kubesolo logger.
func callerMarshal(pc uintptr, file string, line int) string {
	function := ""
	if f := runtime.FuncForPC(pc); f != nil {
		function = f.Name()
	}
	return callerName(function, file, line)
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
