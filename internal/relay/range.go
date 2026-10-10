package relay

import (
	"errors"
	"strconv"
	"strings"
)

var errUnsatisfiable = errors.New("unsatisfiable range")

func parseRange(header string, size int64) (int64, int64, error) {
	if header == "" {
		return 0, size, nil
	}

	spec, ok := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !ok {
		return 0, 0, errors.New("unsupported range")
	}
	if strings.Contains(spec, ",") {
		return 0, 0, errors.New("multi-ranges are not supported")
	}

	from, to, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, errors.New("malformed range")
	}

	if from == "" && to == "" {
		return 0, 0, errors.New("malformed range")
	}

	if from == "" {
		//suffix form, last N bytes
		n, err := strconv.ParseInt(to, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, errors.New("malformed suffix range")
		}
		if n > size {
			n = size
		}
		return size - n, n, nil
	}

	if to == "" {
		// open ended, byte N to EOF
		s, err := strconv.ParseInt(from, 10, 64)
		if err != nil || s < 0 {
			return 0, 0, errors.New("malformed range start")
		}
		if s >= size {
			return 0, 0, errUnsatisfiable
		}
		return s, size - s, nil
	}

	// if we get here, we have an explicit start and end
	s, err1 := strconv.ParseInt(from, 10, 64)
	e, err2 := strconv.ParseInt(to, 10, 64)
	if err1 != nil || err2 != nil || s < 0 || e < s {
		return 0, 0, errors.New("malformed range start")
	}
	if s >= size {
		return 0, 0, errUnsatisfiable
	}
	if e >= size {
		e = size - 1
	}
	return s, e - s + 1, nil
}
