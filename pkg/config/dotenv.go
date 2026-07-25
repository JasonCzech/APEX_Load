package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv loads key=value pairs from a .env file into the process environment.
// Existing environment variables are not overwritten. Blank lines and comment lines
// (prefixed with #) are ignored. Returns nil if the file does not exist.
func LoadDotEnv(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		sep := strings.Index(line, "=")
		if sep < 1 {
			continue
		}

		key := strings.TrimSpace(line[:sep])
		value := line[sep+1:]

		// Strip surrounding quotes. The length guard matters: for a value that is
		// a single quote character, HasPrefix and HasSuffix both match the SAME
		// byte and the slice becomes value[1:0], which panics and kills the
		// process during startup before any logging is configured.
		if len(value) >= 2 &&
			((strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
				(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'"))) {
			value = value[1 : len(value)-1]
		}

		// Only set if not already present
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}
