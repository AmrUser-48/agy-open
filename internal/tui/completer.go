package tui

import (
	"os"
	"path/filepath"
	"strings"
)

// completionPathLimit caps how many path candidates one completion request
// returns, so a huge directory cannot flood the popup.
const completionPathLimit = 200

// mentionCommands are the slash commands whose argument is free-form task
// text. An @path token inside that text is completed as a workspace mention.
var mentionCommands = map[string]bool{
	"/boost":            true,
	"/btw":              true,
	"/codesearch":       true,
	"/goal":             true,
	"/grill-me":         true,
	"/learn":            true,
	"/plan":             true,
	"/teamwork":         true,
	"/teamwork-preview": true,
}

// completePathEntries lists the workspace-relative paths that continue query.
//
// The query is matched literally (it is never treated as a glob pattern),
// resolved against root, and refused when it would leave root, including
// through a symlink. Directories are returned first and end in "/". Hidden
// entries appear only when the final path element starts with ".". Names
// containing whitespace are skipped because completion tokens are split on
// whitespace. The directory prefix is returned exactly as the user typed it.
func completePathEntries(root, query string, directoriesOnly bool) []string {
	if root == "" || filepath.IsAbs(query) {
		return nil
	}
	dirPart, base := "", query
	if i := strings.LastIndex(query, "/"); i >= 0 {
		dirPart, base = query[:i+1], query[i+1:]
	}

	cleaned := filepath.Clean(filepath.FromSlash(dirPart))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return nil
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	realDir, err := filepath.EvalSymlinks(filepath.Join(realRoot, cleaned))
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(realRoot, realDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	entries, err := os.ReadDir(realDir)
	if err != nil {
		return nil
	}

	showHidden := strings.HasPrefix(base, ".")
	lowerBase := strings.ToLower(base)
	var dirs, files []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.ContainsAny(name, " \t\n") {
			continue
		}
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), lowerBase) {
			continue
		}
		isDir := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(realDir, name)); err == nil {
				isDir = info.IsDir()
			}
		}
		if directoriesOnly && !isDir {
			continue
		}
		if isDir {
			dirs = append(dirs, dirPart+name+"/")
		} else {
			files = append(files, dirPart+name)
		}
	}

	paths := append(dirs, files...)
	if len(paths) > completionPathLimit {
		paths = paths[:completionPathLimit]
	}
	return paths
}

// mentionTokenContext describes an @path token under the cursor in text that
// does not start with a slash command. The @ must begin the input or follow
// whitespace, so addresses such as name@example.com are left alone, and the
// cursor must sit after the @.
func mentionTokenContext(input []rune, cursor int) completionTokenContext {
	start := cursor
	for start > 0 && !isCompletionSpace(input[start-1]) {
		start--
	}
	if cursor <= start || start >= len(input) || input[start] != '@' {
		return completionTokenContext{}
	}
	end := cursor
	for end < len(input) && !isCompletionSpace(input[end]) {
		end++
	}
	return completionTokenContext{
		query:    string(input[start:cursor]),
		start:    start,
		end:      end,
		argIndex: -1,
		mention:  true,
		valid:    true,
	}
}

func (u *UI) completionMentionMatches(ctx completionTokenContext) []string {
	if u.agent == nil {
		return nil
	}
	paths := completePathEntries(u.agent.WorkspaceRoot(), strings.TrimPrefix(ctx.query, "@"), false)
	values := make([]string, len(paths))
	for index, path := range paths {
		values[index] = "@" + path
	}
	return u.completionArgumentMatches(ctx, values)
}

// completionTokenText extracts the replaced token from a completion candidate,
// which is the whole input line with the token substituted. The popup shows
// only this text for mentions so a long or multi-line prompt is not repeated
// on every row.
func (u *UI) completionTokenText(ctx completionTokenContext, candidate string) string {
	runes := []rune(candidate)
	suffix := len(u.input) - ctx.end
	if ctx.start < 0 || suffix < 0 || len(runes) < ctx.start+suffix {
		return candidate
	}
	return string(runes[ctx.start : len(runes)-suffix])
}

// finishMentionAccept runs after a mention candidate is applied and the cursor
// sits at the end of the token. A directory re-opens the popup so the user can
// keep descending; a file gets a trailing space so typing can continue.
func (u *UI) finishMentionAccept() {
	if u.cursor > 0 && u.input[u.cursor-1] == '/' {
		u.updateCompletion()
		return
	}
	if u.cursor >= len(u.input) || !isCompletionSpace(u.input[u.cursor]) {
		tail := append([]rune{' '}, u.input[u.cursor:]...)
		u.input = append(u.input[:u.cursor], tail...)
		u.cursor++
	}
}
