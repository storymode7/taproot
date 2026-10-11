package core

import "slices"

// maskCode blanks comments and string literals in lines of source, so the
// call scanner only sees code: a test fixture holding "func run() {" must
// not become a call edge to run. State carries across lines for block
// comments and multi-line strings. Masked runes become spaces, keeping
// column positions intact. Interpolations (JS ${...}, Python f"{...}")
// stay visible, since they are code.
func maskCode(lines []string, lang string) []string {
	const (
		stCode    = iota
		stBlock   // /* ... */
		stString  // "..." or '...' (backslash escapes; Rust may span lines)
		stRaw     // Go `...` (multi-line, no escapes)
		stTmpl    // JS/TS `...` (multi-line, escapes, ${code})
		stTriple  // Python """/''' and Java """ text blocks
		stRustRaw // Rust r#"..."#
		stRegex   // JS/TS /.../ literal (single line)
	)
	python := lang == "python"
	jsLike := lang == "javascript" || lang == "typescript"
	rust := lang == "rust"
	tripleOK := python || lang == "java"

	// An interpolation (${ or f-string {) returns to its string state when
	// the brace depth it opened at closes.
	type interp struct {
		depth int
		state int
		quote rune
	}
	var stack []interp

	st := stCode
	var quote rune // closing quote for stString / stTriple
	fstring := false
	hashes := 0 // stRustRaw closing # count
	inClass := false
	braces := 0

	out := make([]string, len(lines))
	for li, line := range lines {
		rs := []rune(line)
		n := len(rs)
		if (st == stString && !rust) || st == stRegex {
			st = stCode // unterminated single-line literal ends at EOL
			stack = stack[:0]
		}
		var prev rune // last non-space code rune on this line
		for i := 0; i < n; i++ {
			c := rs[i]
			next := rune(0)
			if i+1 < n {
				next = rs[i+1]
			}
			wasCode := st == stCode
			switch st {
			case stCode:
				switch {
				case python && c == '#', !python && c == '/' && next == '/' && !(jsLike && urlSlashes(rs, i)):
					for ; i < n; i++ {
						rs[i] = ' '
					}
				case !python && c == '/' && next == '*':
					rs[i], rs[i+1] = ' ', ' '
					i++
					st = stBlock
				case jsLike && c == '/' && regexCanStart(rs, i, prev):
					rs[i] = ' '
					inClass = false
					st = stRegex
				case tripleOK && (c == '"' || (python && c == '\'')) && next == c && i+2 < n && rs[i+2] == c:
					quote = c
					rs[i], rs[i+1], rs[i+2] = ' ', ' ', ' '
					i += 2
					st = stTriple
				case rust && c == 'r' && rustRawStart(rs, i) > 0:
					hashes = rustRawStart(rs, i) - 1
					for k := 0; k <= hashes+1; k++ {
						rs[i+k] = ' '
					}
					i += hashes + 1
					st = stRustRaw
				case c == '`' && lang == "go":
					rs[i] = ' '
					st = stRaw
				case c == '`' && jsLike:
					rs[i] = ' '
					st = stTmpl
				case (c == '"' || (c == '\'' && !isLifetime(rs, i, lang))) && (!jsLike || jsQuoteOpens(rs, i)):
					quote = c
					fstring = python && isFPrefix(rs, i)
					rs[i] = ' '
					st = stString
				case c == '{':
					braces++
				case c == '}':
					if k := len(stack); k > 0 && stack[k-1].depth == braces {
						top := stack[k-1]
						stack = stack[:k-1]
						rs[i] = ' '
						st, quote = top.state, top.quote
						fstring = st == stString
					} else {
						braces--
					}
				}
				if c != ' ' && c != '\t' && st == stCode {
					prev = c
				}
			case stBlock:
				if c == '*' && next == '/' {
					rs[i+1] = ' '
					i++
					st = stCode
				}
				rs[i] = ' '
			case stString:
				switch {
				case c == '\\' && i+1 < n:
					rs[i+1] = ' '
					i++
				case fstring && c == '{' && next == '{':
					rs[i+1] = ' '
					i++
				case fstring && c == '{':
					stack = append(stack, interp{braces, stString, quote})
					st = stCode
				case c == quote:
					st = stCode
				}
				rs[i] = ' '
			case stRaw:
				if c == '`' {
					st = stCode
				}
				rs[i] = ' '
			case stTmpl:
				switch {
				case c == '\\' && i+1 < n:
					rs[i], rs[i+1] = ' ', ' '
					i++
				case c == '`':
					rs[i] = ' '
					st = stCode
				case c == '$' && next == '{':
					rs[i], rs[i+1] = ' ', ' '
					i++
					stack = append(stack, interp{braces, stTmpl, 0})
					st = stCode
				default:
					rs[i] = ' '
				}
			case stTriple:
				if c == '\\' && i+1 < n {
					rs[i], rs[i+1] = ' ', ' '
					i++
					continue
				}
				if c == quote && next == quote && i+2 < n && rs[i+2] == quote {
					rs[i], rs[i+1], rs[i+2] = ' ', ' ', ' '
					i += 2
					st = stCode
					continue
				}
				rs[i] = ' '
			case stRustRaw:
				if c == '"' && i+hashes < n && allHashes(rs[i+1:i+1+hashes]) {
					for k := 0; k <= hashes; k++ {
						rs[i+k] = ' '
					}
					i += hashes
					st = stCode
					continue
				}
				rs[i] = ' '
			case stRegex:
				switch {
				case c == '\\' && i+1 < n:
					rs[i+1] = ' '
					i++
				case c == '[':
					inClass = true
				case c == ']':
					inClass = false
				case c == '/' && !inClass:
					st = stCode
				}
				rs[i] = ' '
			}
			if !wasCode && st == stCode {
				prev = ')' // a closed literal is an operand: a following / divides
			}
		}
		out[li] = string(rs)
	}
	return out
}

// isLifetime reports whether the ' at rs[i] opens a Rust lifetime or label
// ('a, 'static) rather than a char literal ('a', '\n').
func isLifetime(rs []rune, i int, lang string) bool {
	if lang != "rust" {
		return false
	}
	if i+1 >= len(rs) || !isIdentStart(rs[i+1]) {
		return false
	}
	return i+2 >= len(rs) || rs[i+2] != '\''
}

// regexCanStart reports whether a / at rs[i] opens a JS regex literal rather
// than dividing: it must follow an operator, an opening bracket, a line
// start, or a keyword like return. After '<' or '}' it is a JSX closing or
// self-closing tag (</p>, key={k} />) or division, and so is a /> that opens
// its line unless another / follows to close a regex.
func regexCanStart(rs []rune, i int, prev rune) bool {
	if i+1 < len(rs) && (rs[i+1] == '/' || rs[i+1] == '*') {
		return false
	}
	if prev == 0 && i+1 < len(rs) && rs[i+1] == '>' && !slices.Contains(rs[i+2:], '/') {
		return false
	}
	switch prev {
	case 0, '(', ',', '=', ':', '[', '!', '&', '|', '?', '{', ';', '+', '-', '*', '%', '>', '~', '^':
		return true
	}
	if isIdentPart(prev) {
		j := i - 1
		for j >= 0 && (rs[j] == ' ' || rs[j] == '\t') {
			j--
		}
		end := j + 1
		for j >= 0 && isIdentPart(rs[j]) {
			j--
		}
		switch string(rs[j+1 : end]) {
		case "return", "typeof", "case", "in", "of", "yield", "await", "void", "delete":
			return true
		}
	}
	return false
}

// urlSlashes reports whether the // at rs[i] is the scheme separator of a
// URL written as JSX text (https://example.com), not a line comment.
func urlSlashes(rs []rune, i int) bool {
	if i < 2 || rs[i-1] != ':' || i+2 >= len(rs) || !isIdentPart(rs[i+2]) {
		return false
	}
	j := i - 2
	for j >= 0 && (rs[j] >= 'a' && rs[j] <= 'z' || rs[j] >= 'A' && rs[j] <= 'Z') {
		j--
	}
	return j < i-2 && (j < 0 || !isIdentPart(rs[j]))
}

// jsQuoteOpens reports whether a quote at rs[i] in JS/TS code opens a string
// literal. A JS string closes on its own line (or continues with a trailing
// \), and nothing but a keyword sits directly before one, so an apostrophe or
// inch mark in JSX text (Can't, 12") is text.
func jsQuoteOpens(rs []rune, i int) bool {
	if i > 0 && isIdentPart(rs[i-1]) {
		j := i - 1
		for j >= 0 && isIdentPart(rs[j]) {
			j--
		}
		switch string(rs[j+1 : i]) {
		case "return", "typeof", "case", "in", "of", "yield", "await", "void", "delete",
			"throw", "else", "do", "instanceof", "from", "import", "export", "default":
		default:
			return false
		}
	}
	for j := i + 1; j < len(rs); j++ {
		switch rs[j] {
		case '\\':
			if j+1 == len(rs) {
				return true
			}
			j++
		case rs[i]:
			return true
		}
	}
	return false
}

// rustRawStart returns 1 + the number of # when rs[i:] opens a raw string
// (r"…", r#"…"#, br"…"), else 0.
func rustRawStart(rs []rune, i int) int {
	if i > 0 && isIdentPart(rs[i-1]) && !(rs[i-1] == 'b' && (i < 2 || !isIdentPart(rs[i-2]))) {
		return 0
	}
	j := i + 1
	for j < len(rs) && rs[j] == '#' {
		j++
	}
	if j < len(rs) && rs[j] == '"' {
		return j - i
	}
	return 0
}

func allHashes(rs []rune) bool {
	for _, r := range rs {
		if r != '#' {
			return false
		}
	}
	return true
}

// isFPrefix reports whether the quote at rs[i] opens a Python f-string
// (f"…", rf"…", F'…').
func isFPrefix(rs []rune, i int) bool {
	for j := i - 1; j >= 0 && j >= i-2; j-- {
		switch rs[j] {
		case 'f', 'F':
			return j == 0 || !isIdentPart(rs[j-1]) || rs[j-1] == 'r' || rs[j-1] == 'R'
		case 'r', 'R', 'b', 'B':
			continue
		default:
			return false
		}
	}
	return false
}
