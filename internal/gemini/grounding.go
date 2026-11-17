package gemini

import (
	"fmt"
	"net/url"
	"strings"
)

// GroundingMetadata is the native Gemini Search grounding result. It is kept
// outside Content.Parts and excluded from serialized conversation history.
type GroundingMetadata struct {
	WebSearchQueries  []string          `json:"webSearchQueries,omitempty"`
	SearchEntryPoint  *SearchEntryPoint `json:"searchEntryPoint,omitempty"`
	GroundingChunks   []GroundingChunk  `json:"groundingChunks,omitempty"`
	GroundingSupports []GroundingSupport `json:"groundingSupports,omitempty"`
}

type SearchEntryPoint struct {
	RenderedContent string `json:"renderedContent,omitempty"`
	SDKBlob         string `json:"sdkBlob,omitempty"`
}

type GroundingChunk struct {
	Web *GroundingWeb `json:"web,omitempty"`
}

type GroundingWeb struct {
	URI   string `json:"uri,omitempty"`
	Title string `json:"title,omitempty"`
}

type GroundingSupport struct {
	Segment              GroundingSegment `json:"segment"`
	GroundingChunkIndices []int            `json:"groundingChunkIndices,omitempty"`
}

type GroundingSegment struct {
	StartIndex int    `json:"startIndex,omitempty"`
	EndIndex   int    `json:"endIndex,omitempty"`
	Text       string `json:"text,omitempty"`
}

// MergeGroundingMetadata combines the incremental metadata returned by SSE
// chunks while preserving the provider's final Search suggestion payload.
func MergeGroundingMetadata(dst **GroundingMetadata, src *GroundingMetadata) {
	if dst == nil || src == nil {
		return
	}
	if *dst == nil {
		copied := *src
		copied.WebSearchQueries = append([]string(nil), src.WebSearchQueries...)
		copied.GroundingChunks = append([]GroundingChunk(nil), src.GroundingChunks...)
		copied.GroundingSupports = append([]GroundingSupport(nil), src.GroundingSupports...)
		if src.SearchEntryPoint != nil {
			entry := *src.SearchEntryPoint
			copied.SearchEntryPoint = &entry
		}
		*dst = &copied
		return
	}
	current := *dst
	for _, query := range src.WebSearchQueries {
		if !containsString(current.WebSearchQueries, query) {
			current.WebSearchQueries = append(current.WebSearchQueries, query)
		}
	}
	chunkIndexMap := make([]int, len(src.GroundingChunks))
	for sourceIndex, chunk := range src.GroundingChunks {
		mappedIndex := -1
		uri := ""
		if chunk.Web != nil {
			uri = chunk.Web.URI
		}
		for existingIndex, existing := range current.GroundingChunks {
			if uri != "" && existing.Web != nil && existing.Web.URI == uri {
				mappedIndex = existingIndex
				break
			}
		}
		if mappedIndex < 0 {
			mappedIndex = len(current.GroundingChunks)
			current.GroundingChunks = append(current.GroundingChunks, chunk)
		}
		chunkIndexMap[sourceIndex] = mappedIndex
	}
	for _, sourceSupport := range src.GroundingSupports {
		support := sourceSupport
		support.GroundingChunkIndices = make([]int, 0, len(sourceSupport.GroundingChunkIndices))
		for _, sourceIndex := range sourceSupport.GroundingChunkIndices {
			if sourceIndex >= 0 && sourceIndex < len(chunkIndexMap) {
				support.GroundingChunkIndices = append(support.GroundingChunkIndices, chunkIndexMap[sourceIndex])
			}
		}
		duplicate := false
		for _, existing := range current.GroundingSupports {
			if existing.Segment.StartIndex == support.Segment.StartIndex &&
				existing.Segment.EndIndex == support.Segment.EndIndex &&
				existing.Segment.Text == support.Segment.Text &&
				equalIntSlices(existing.GroundingChunkIndices, support.GroundingChunkIndices) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			current.GroundingSupports = append(current.GroundingSupports, support)
		}
	}
	if src.SearchEntryPoint != nil &&
		(src.SearchEntryPoint.RenderedContent != "" || src.SearchEntryPoint.SDKBlob != "") {
		entry := *src.SearchEntryPoint
		current.SearchEntryPoint = &entry
	}
}

func equalIntSlices(left, right []int) bool {
	if len(left) != len(right) { return false }
	for i := range left { if left[i] != right[i] { return false } }
	return true
}

func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

// DisplayText renders Google Search suggestions, claim-to-source associations,
// and aggregate source links in a terminal-friendly form.
func (m *GroundingMetadata) DisplayText() string {
	if m == nil {
		return ""
	}
	var lines []string
	if m.SearchEntryPoint != nil || len(m.WebSearchQueries) > 0 {
		lines = append(lines, "Google Search suggestions:")
		queries := make([]string, 0, len(m.WebSearchQueries))
		for _, query := range m.WebSearchQueries {
			query = strings.TrimSpace(query)
			if query != "" && !containsString(queries, query) {
				queries = append(queries, query)
			}
		}
		if len(queries) == 0 {
			lines = append(lines, "  Google Search")
		} else {
			for _, query := range queries {
				lines = append(lines, fmt.Sprintf("  %s — https://www.google.com/search?q=%s", query, url.QueryEscape(query)))
			}
		}
	}

	var sources []GroundingWeb
	for _, chunk := range m.GroundingChunks {
		if chunk.Web == nil || strings.TrimSpace(chunk.Web.URI) == "" {
			continue
		}
		duplicate := false
		for _, existing := range sources {
			if existing.URI == chunk.Web.URI {
				duplicate = true
				break
			}
		}
		if !duplicate {
			sources = append(sources, *chunk.Web)
		}
	}
	if len(sources) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "Sources:")
		for i, source := range sources {
			title := strings.TrimSpace(source.Title)
			if title == "" {
				title = source.URI
			}
			lines = append(lines, fmt.Sprintf("  [%d] %s — %s", i+1, title, source.URI))
		}
	}
	if len(m.GroundingSupports) > 0 && len(sources) > 0 {
		var claims []string
		for _, support := range m.GroundingSupports {
			claim := strings.TrimSpace(support.Segment.Text)
			if claim == "" || len(support.GroundingChunkIndices) == 0 {
				continue
			}
			refs := make([]string, 0, len(support.GroundingChunkIndices))
			for _, index := range support.GroundingChunkIndices {
				if index >= 0 && index < len(m.GroundingChunks) {
					web := m.GroundingChunks[index].Web
					if web == nil || strings.TrimSpace(web.URI) == "" {
						continue
					}
					for sourceIndex, source := range sources {
						if source.URI == web.URI {
							ref := fmt.Sprintf("[%d]", sourceIndex+1)
							if !containsString(refs, ref) {
								refs = append(refs, ref)
							}
							break
						}
					}
				}
			}
			if len(refs) > 0 {
				claims = append(claims, fmt.Sprintf("  %s → %s", claim, strings.Join(refs, " ")))
			}
		}
		if len(claims) > 0 {
			lines = append(lines, "", "Grounded claims:")
			lines = append(lines, claims...)
		}
	}
	return strings.Join(lines, "\n")
}
