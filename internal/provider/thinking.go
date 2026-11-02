package provider

import "strings"

// Some models (Gemma, DeepSeek-R1 style, Qwen, ...) write their reasoning into the
// normal text between tags such as <thought>...</thought>. splitter separates it
// from the answer while the text arrives in arbitrary pieces, so a tag may be cut in two.
type splitter struct {
	inThink bool
	hold    string // the tail that may be the start of a tag
}

var openTags = []string{"<thought>", "<thinking>", "<think>", "<reasoning>"}
var closeTags = []string{"</thought>", "</thinking>", "</think>", "</reasoning>"}

// longestPrefixAtEnd: how many trailing bytes of s are the beginning of one of the tags.
func longestPrefixAtEnd(s string, tags []string) int {
	best := 0
	for _, t := range tags {
		for n := min(len(t)-1, len(s)); n > best; n-- {
			if strings.HasSuffix(s, t[:n]) {
				best = n
				break
			}
		}
	}
	return best
}

func indexTag(s string, tags []string) (idx, n int) {
	idx = -1
	for _, t := range tags {
		if i := strings.Index(s, t); i >= 0 && (idx < 0 || i < idx) {
			idx, n = i, len(t)
		}
	}
	return idx, n
}

// feed takes a piece of text and returns the answer part and the reasoning part.
func (s *splitter) feed(piece string) (text, think string) {
	buf := s.hold + piece
	s.hold = ""
	var tb, kb strings.Builder
	for buf != "" {
		tags := openTags
		if s.inThink {
			tags = closeTags
		}
		i, n := indexTag(buf, tags)
		if i < 0 {
			keep := longestPrefixAtEnd(buf, tags)
			s.hold = buf[len(buf)-keep:]
			buf = buf[:len(buf)-keep]
			if s.inThink {
				kb.WriteString(buf)
			} else {
				tb.WriteString(buf)
			}
			break
		}
		if s.inThink {
			kb.WriteString(buf[:i])
		} else {
			tb.WriteString(buf[:i])
		}
		buf = buf[i+n:]
		s.inThink = !s.inThink
	}
	return tb.String(), kb.String()
}

// flush returns what was held back at the end of the stream (it was not a tag after all).
func (s *splitter) flush() (text, think string) {
	h := s.hold
	s.hold = ""
	if s.inThink {
		return "", h
	}
	return h, ""
}
