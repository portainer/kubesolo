package system

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// MemTotal returns the host's memory in bytes, or 0 if it cannot be read.
func MemTotal() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	return parseMemTotal(f)
}

func parseMemTotal(r io.Reader) uint64 {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024
		}
	}
	return 0
}
