package logfiles

import "encoding/json"

// The scanner's reading of a Codex rollout line: the same codexLine encoding/json fills, field for field.
// Keys that are not listed are skipped undecoded.

var (
	codexLineFields     = fieldNames("type", "timestamp", "payload")
	codexPayloadFields  = fieldNames("type", "id", "cwd", "thread_source", "forked_from_id", "model", "name", "arguments", "call_id", "output", "response_id", "usage", "thread_settings", "info", "item")
	codexUsageFields    = fieldNames("input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens")
	codexSettingsFields = fieldNames("model")
	codexInfoFields     = fieldNames("total_token_usage", "last_token_usage")
	codexItemFields     = fieldNames("type", "id", "server", "tool", "kind", "arguments", "result", "error", "status", "command", "aggregated_output", "exit_code", "duration", "changes", "query", "path", "content")
	codexDurationFields = fieldNames("secs", "nanos")
	codexContentFields  = fieldNames("type", "text")
)

func scanCodexLine(line []byte, l *codexLine) bool {
	s := scanner{b: line}
	s.ws()
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "type":
			return m.first(0) && s.setText(&l.Type)
		case "timestamp":
			return m.first(1) && s.setText(&l.Timestamp)
		case "payload":
			return m.first(2) && s.codexPayload(&l.Payload)
		}
		return !foldsToField(key, codexLineFields) && s.value()
	}) && s.done()
}

func (s *scanner) codexPayload(p *codexPayload) bool {
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "type":
			return m.first(0) && s.setText(&p.Type)
		case "id":
			return m.first(1) && s.setText(&p.ID)
		case "cwd":
			return m.first(2) && s.setText(&p.Cwd)
		case "thread_source":
			return m.first(3) && s.setText(&p.ThreadSource)
		case "forked_from_id":
			return m.first(4) && s.setText(&p.ForkedFrom)
		case "model":
			return m.first(5) && s.setText(&p.Model)
		case "name":
			return m.first(6) && s.setText(&p.Name)
		case "arguments":
			return m.first(7) && s.setSize((*int)(&p.Arguments))
		case "call_id":
			return m.first(8) && s.setText(&p.CallID)
		case "output":
			return m.first(9) && s.setSize((*int)(&p.Output))
		case "response_id":
			return m.first(10) && s.setText(&p.ResponseID)
		case "usage":
			if !m.first(11) {
				return false
			}
			p.Usage = new(codexUsage)
			return s.codexUsage(p.Usage)
		case "thread_settings":
			if !m.first(12) {
				return false
			}
			p.Settings = new(codexSettings)
			var t seen
			return s.object(func(key []byte) bool {
				if string(key) == "model" {
					return t.first(0) && s.setText(&p.Settings.Model)
				}
				return !foldsToField(key, codexSettingsFields) && s.value()
			})
		case "info":
			if !m.first(13) {
				return false
			}
			p.Info = new(codexInfo)
			var t seen
			return s.object(func(key []byte) bool {
				switch string(key) {
				case "total_token_usage":
					if !t.first(0) {
						return false
					}
					p.Info.Total = new(codexUsage)
					return s.codexUsage(p.Info.Total)
				case "last_token_usage":
					if !t.first(1) {
						return false
					}
					p.Info.Last = new(codexUsage)
					return s.codexUsage(p.Info.Last)
				}
				return !foldsToField(key, codexInfoFields) && s.value()
			})
		case "item":
			if !m.first(14) {
				return false
			}
			p.Item = new(codexItemLog)
			return s.codexItemLog(p.Item)
		}
		return !foldsToField(key, codexPayloadFields) && s.value()
	})
}

func (s *scanner) codexUsage(u *codexUsage) bool {
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "input_tokens":
			return m.first(0) && s.setInt64(&u.Input)
		case "cached_input_tokens":
			return m.first(1) && s.setInt64(&u.Cached)
		case "cache_write_input_tokens":
			return m.first(2) && s.setInt64(&u.CacheW)
		case "output_tokens":
			return m.first(3) && s.setInt64(&u.Output)
		case "reasoning_output_tokens":
			return m.first(4) && s.setInt64(&u.Reasoning)
		}
		return !foldsToField(key, codexUsageFields) && s.value()
	})
}

func (s *scanner) codexItemLog(it *codexItemLog) bool {
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "type":
			return m.first(0) && s.setText(&it.Type)
		case "id":
			return m.first(1) && s.setText(&it.ID)
		case "server":
			return m.first(2) && s.setText(&it.Server)
		case "tool":
			return m.first(3) && s.setText(&it.Tool)
		case "kind":
			return m.first(4) && s.setText(&it.Kind)
		case "arguments":
			return m.first(5) && s.setSize((*int)(&it.Arguments))
		case "result":
			return m.first(6) && s.setSize((*int)(&it.Result))
		case "error":
			if !m.first(7) {
				return false
			}
			start := s.i
			if !s.value() {
				return false
			}
			it.Error = json.RawMessage(append([]byte(nil), s.b[start:s.i]...))
			return true
		case "status":
			return m.first(8) && s.setText(&it.Status)
		case "command":
			return m.first(9) && s.setSize((*int)(&it.Command))
		case "aggregated_output":
			return m.first(10) && s.setTextLen(&it.Output)
		case "exit_code":
			if !m.first(11) {
				return false
			}
			n, ok := s.int64()
			code := int(n)
			it.ExitCode = &code
			return ok && int64(code) == n
		case "duration":
			if !m.first(12) {
				return false
			}
			it.Duration = new(codexDuration)
			var d seen
			return s.object(func(key []byte) bool {
				switch string(key) {
				case "secs":
					return d.first(0) && s.setFloat64(&it.Duration.Secs)
				case "nanos":
					return d.first(1) && s.setFloat64(&it.Duration.Nanos)
				}
				return !foldsToField(key, codexDurationFields) && s.value()
			})
		case "changes":
			return m.first(13) && s.setSize((*int)(&it.Changes))
		case "query":
			return m.first(14) && s.setText(&it.Query)
		case "path":
			return m.first(15) && s.setText(&it.Path)
		case "content":
			if !m.first(16) {
				return false
			}
			it.Content = []codexContent{} // an empty list is an empty slice, as encoding/json decodes it
			return s.array(func() bool {
				var c codexContent
				var t seen
				ok := s.object(func(key []byte) bool {
					switch string(key) {
					case "type":
						return t.first(0) && s.setText(&c.Type)
					case "text":
						return t.first(1) && s.setText(&c.Text)
					}
					return !foldsToField(key, codexContentFields) && s.value()
				})
				it.Content = append(it.Content, c)
				return ok
			})
		}
		return !foldsToField(key, codexItemFields) && s.value()
	})
}
