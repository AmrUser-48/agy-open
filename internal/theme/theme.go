package theme

type Theme struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

var Themes = []Theme{
	{Name: "catppuccin", Description: "Catppuccin Macchiato/Mocha dark theme"},
	{Name: "monokai", Description: "Monokai vibrant code theme"},
	{Name: "gruvbox", Description: "Gruvbox retro warm dark theme"},
	{Name: "solarized", Description: "Solarized dark theme"},
	{Name: "tokyo-night", Description: "Tokyo Night cool neon theme"},
	{Name: "dracula", Description: "Dracula high-contrast dark theme"},
	{Name: "dark", Description: "Pure dark theme"},
	{Name: "lcd7s", Description: "LCD 7s classic terminal theme"},
}

func ListNames() []string {
	names := make([]string, len(Themes))
	for i, t := range Themes {
		names[i] = t.Name
	}
	return names
}

func IsValid(name string) bool {
	for _, t := range Themes {
		if t.Name == name {
			return true
		}
	}
	return false
}

// ANSI Color helper inspired by aichat
func ColorText(text string, ansiColorCode string) string {
	return "\u001b[" + ansiColorCode + "m" + text + "\u001b[0m"
}

func Muted(text string) string {
	return ColorText(text, "90") // Dark gray / muted
}

func UserPromptColor(text string) string {
	return ColorText(text, "34") // Blue
}

func AssistantPromptColor(text string) string {
	return ColorText(text, "32") // Green
}
