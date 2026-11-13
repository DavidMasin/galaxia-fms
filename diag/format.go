package diag

import "fmt"

func formatFinding(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
