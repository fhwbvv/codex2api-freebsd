package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const responseContextJSONWireExpansion = int64(6)

// NormalizeResponseContextItems keeps JSON structure, ordering, whitespace,
// and number spelling intact while canonicalizing string escapes that
// encoding/json expands for HTML/JSONP safety. The normalized byte form is the
// logical representation used by both local admission and Redis hydration.
func NormalizeResponseContextItems(items []json.RawMessage) ([]json.RawMessage, error) {
	var normalized []json.RawMessage
	for index, item := range items {
		normalizedItem, changed, err := normalizeResponseContextItem(item)
		if err != nil {
			return nil, fmt.Errorf("response context item %d: %w", index, err)
		}
		if changed && normalized == nil {
			normalized = append([]json.RawMessage(nil), items...)
		}
		if normalized != nil {
			normalized[index] = normalizedItem
		}
	}
	if normalized == nil {
		return items, nil
	}
	return normalized, nil
}

func normalizeResponseContextItem(item json.RawMessage) (json.RawMessage, bool, error) {
	if !json.Valid(item) {
		return nil, false, fmt.Errorf("invalid JSON")
	}
	if !responseContextItemNeedsStringNormalization(item) {
		return item, false, nil
	}

	var output bytes.Buffer
	output.Grow(len(item))
	last := 0
	changed := false
	for index := 0; index < len(item); {
		if item[index] != '"' {
			index++
			continue
		}
		end, err := responseContextJSONStringEnd(item, index)
		if err != nil {
			return nil, false, err
		}
		var decoded string
		if err := json.Unmarshal(item[index:end], &decoded); err != nil {
			return nil, false, err
		}
		encoded, err := encodeResponseContextJSONString(decoded)
		if err != nil {
			return nil, false, err
		}
		if !bytes.Equal(encoded, item[index:end]) {
			if !changed {
				output.Write(item[:index])
				changed = true
			} else {
				output.Write(item[last:index])
			}
			output.Write(encoded)
			last = end
		}
		index = end
	}
	if !changed {
		return item, false, nil
	}
	output.Write(item[last:])
	return json.RawMessage(output.Bytes()), true, nil
}

func responseContextItemNeedsStringNormalization(item []byte) bool {
	return bytes.ContainsAny(item, "<>&") ||
		bytes.Contains(item, []byte("\u2028")) ||
		bytes.Contains(item, []byte("\u2029")) ||
		bytes.Contains(item, []byte(`\u`)) ||
		bytes.Contains(item, []byte(`\U`))
}

func responseContextJSONStringEnd(item []byte, start int) (int, error) {
	escaped := false
	for index := start + 1; index < len(item); index++ {
		switch {
		case escaped:
			escaped = false
		case item[index] == '\\':
			escaped = true
		case item[index] == '"':
			return index + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated JSON string")
}

func encodeResponseContextJSONString(value string) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(output.Bytes(), []byte{'\n'})
	return restoreResponseContextJSONPSeparatorEscapes(encoded), nil
}

func restoreResponseContextJSONPSeparatorEscapes(encoded []byte) []byte {
	var restored []byte
	last := 0
	for index := 0; index+len(`\u2028`) <= len(encoded); index++ {
		if encoded[index] != '\\' || encoded[index+1] != 'u' ||
			(!bytes.Equal(encoded[index+2:index+6], []byte("2028")) &&
				!bytes.Equal(encoded[index+2:index+6], []byte("2029"))) {
			continue
		}

		precedingBackslashes := 0
		for previous := index - 1; previous >= 0 && encoded[previous] == '\\'; previous-- {
			precedingBackslashes++
		}
		if precedingBackslashes%2 != 0 {
			continue
		}

		if restored == nil {
			restored = make([]byte, 0, len(encoded))
		}
		restored = append(restored, encoded[last:index]...)
		if encoded[index+5] == '8' {
			restored = append(restored, "\u2028"...)
		} else {
			restored = append(restored, "\u2029"...)
		}
		last = index + len(`\u2028`)
		index = last - 1
	}
	if restored == nil {
		return encoded
	}
	return append(restored, encoded[last:]...)
}

// ResponseContextWireLimit returns a proven upper bound for encoding/json's
// Redis record representation of at most maxItems normalized raw items whose
// total logical bytes do not exceed maxLogicalBytes. A literal '<', '>', or
// '&' is the worst case: one logical byte becomes a six-byte \u00xx escape.
func ResponseContextWireLimit(maxLogicalBytes int64, maxItems int) int64 {
	if maxLogicalBytes < 0 {
		maxLogicalBytes = 0
	}
	if maxItems < 1 {
		maxItems = 1
	}
	const wrapperBytes = int64(len(`{"items":[`) + len(`]}`))
	overhead := wrapperBytes + int64(maxItems-1)
	maxInt64 := int64(^uint64(0) >> 1)
	if maxLogicalBytes > (maxInt64-overhead)/responseContextJSONWireExpansion {
		return maxInt64
	}
	return maxLogicalBytes*responseContextJSONWireExpansion + overhead
}
