package tool

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gal-cli/gal-cli/internal/provider"
)

func (r *Registry) registerPatch() {
	r.Register(provider.ToolDef{
		Name:        "file_patch",
		Description: "Edit a file by replacing an exact string match. More precise than file_edit (line-based). The old_str must match exactly one location in the file. Use for surgical edits where you know the exact text to change.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path to edit"},
				"old_str": map[string]any{"type": "string", "description": "Exact string to find (must be unique in file)"},
				"new_str": map[string]any{"type": "string", "description": "Replacement string"},
			},
			"required": []string{"path", "old_str", "new_str"},
		},
	}, func(_ context.Context, args map[string]any) (string, error) {
		p, _ := args["path"].(string)
		oldStr, _ := args["old_str"].(string)
		newStr, _ := args["new_str"].(string)

		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		content := string(data)

		count := strings.Count(content, oldStr)
		if count == 0 {
			return "", fmt.Errorf("old_str not found in %s", p)
		}
		if count > 1 {
			return "", fmt.Errorf("old_str matches %d locations in %s (must be unique)", count, p)
		}

		newContent := strings.Replace(content, oldStr, newStr, 1)
		if err := os.WriteFile(p, []byte(newContent), 0644); err != nil {
			return "", err
		}

		return fmt.Sprintf("patched %s\n%s", p, FormatDiff(oldStr, newStr)), nil
	})
}

// FormatDiff produces a compact diff between old and new text.
// Lines prefixed with - (removed) and + (added).
func FormatDiff(oldStr, newStr string) string {
	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")

	var sb strings.Builder
	// find common prefix/suffix to minimize diff output
	prefix := commonPrefix(oldLines, newLines)
	suffix := commonSuffix(oldLines[prefix:], newLines[prefix:])

	oldMid := oldLines[prefix : len(oldLines)-suffix]
	newMid := newLines[prefix : len(newLines)-suffix]

	if prefix > 0 {
		sb.WriteString(fmt.Sprintf(" ... (%d unchanged lines)\n", prefix))
	}
	for _, l := range oldMid {
		sb.WriteString("- " + l + "\n")
	}
	for _, l := range newMid {
		sb.WriteString("+ " + l + "\n")
	}
	if suffix > 0 {
		sb.WriteString(fmt.Sprintf(" ... (%d unchanged lines)\n", suffix))
	}

	return strings.TrimRight(sb.String(), "\n")
}

func commonPrefix(a, b []string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func commonSuffix(a, b []string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[len(a)-1-i] != b[len(b)-1-i] {
			return i
		}
	}
	return n
}
