package agent

import "github.com/AmrUser-48/agy-open/internal/gemini"

// splitThoughtParts keeps provider thought summaries out of conversation history.
// They may be displayed when verbose output is enabled, but never fed back as
// ordinary assistant text on the next model turn.
func splitThoughtParts(content gemini.Content) (gemini.Content, []gemini.Part) {
	filtered := content
	filtered.Parts = make([]gemini.Part, 0, len(content.Parts))
	var thoughts []gemini.Part
	for _, part := range content.Parts {
		if part.Thought {
			if part.Text != "" {
				thoughts = append(thoughts, part)
			}
			part.Text = ""
			part.Thought = false
			if part.FunctionCall == nil && part.FunctionResponse == nil && part.ThoughtSignature == "" {
				continue
			}
		}
		filtered.Parts = append(filtered.Parts, part)
	}
	return filtered, thoughts
}
