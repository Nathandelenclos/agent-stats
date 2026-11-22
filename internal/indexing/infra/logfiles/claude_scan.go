package logfiles

// The scanner's reading of a Claude Code line: the same claudeLine encoding/json fills, field for field,
// except that the message content stays raw until resolve. Keys that are not listed are skipped undecoded.

var (
	claudeLineFields   = fieldNames("type", "timestamp", "sessionId", "cwd", "isMeta", "isApiErrorMessage", "requestRef", "id", "uuid", "customTitle", "message")
	claudeMessageField = fieldNames("id", "model", "stop_reason", "content", "usage")
	claudeUsageFields  = fieldNames("input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cache_creation", "output_tokens_details")
	claudeCacheFields  = fieldNames("ephemeral_1h_input_tokens")
	claudeOutputFields = fieldNames("thinking_tokens")
	claudeBlockFields  = fieldNames("type", "text", "id", "name", "input", "tool_use_id", "content", "is_error")
)

func scanClaudeLine(line []byte, l *claudeLine) bool {
	s := scanner{b: line}
	s.ws()
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "type":
			return m.first(0) && s.setText(&l.Type)
		case "timestamp":
			return m.first(1) && s.setText(&l.Timestamp)
		case "sessionId":
			return m.first(2) && s.setText(&l.SessionID)
		case "cwd":
			return m.first(3) && s.setText(&l.Cwd)
		case "isMeta":
			return m.first(4) && s.setBool(&l.IsMeta)
		case "isApiErrorMessage":
			return m.first(5) && s.setBool(&l.IsAPIErrorMessage)
		case "requestRef":
			return m.first(6) && s.setText(&l.RequestRef)
		case "id":
			return m.first(7) && s.setText(&l.ID)
		case "uuid":
			return m.first(8) && s.setText(&l.UUID)
		case "customTitle":
			return m.first(9) && s.setText(&l.CustomTitle)
		case "message":
			if !m.first(10) {
				return false
			}
			l.Message = new(claudeMessage)
			return s.claudeMessage(l.Message)
		}
		return !foldsToField(key, claudeLineFields) && s.value()
	}) && s.done()
}

func (s *scanner) claudeMessage(msg *claudeMessage) bool {
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "id":
			return m.first(0) && s.setText(&msg.ID)
		case "model":
			return m.first(1) && s.setText(&msg.Model)
		case "stop_reason":
			return m.first(2) && s.setText(&msg.StopReason)
		case "content":
			if !m.first(3) {
				return false
			}
			start := s.i
			if !s.value() {
				return false
			}
			msg.Content = claudeContent{raw: s.b[start:s.i]}
			return true
		case "usage":
			if !m.first(4) {
				return false
			}
			msg.Usage = new(claudeUsage)
			return s.claudeUsage(msg.Usage)
		}
		return !foldsToField(key, claudeMessageField) && s.value()
	})
}

func (s *scanner) claudeUsage(u *claudeUsage) bool {
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "input_tokens":
			return m.first(0) && s.setInt64(&u.Input)
		case "output_tokens":
			return m.first(1) && s.setInt64(&u.Output)
		case "cache_read_input_tokens":
			return m.first(2) && s.setInt64(&u.CacheRead)
		case "cache_creation_input_tokens":
			return m.first(3) && s.setInt64(&u.CacheCreation)
		case "cache_creation":
			if !m.first(4) {
				return false
			}
			u.CacheDetail = new(claudeCacheDetail)
			var c seen
			return s.object(func(key []byte) bool {
				if string(key) == "ephemeral_1h_input_tokens" {
					return c.first(0) && s.setInt64(&u.CacheDetail.OneHour)
				}
				return !foldsToField(key, claudeCacheFields) && s.value()
			})
		case "output_tokens_details":
			if !m.first(5) {
				return false
			}
			u.OutputDetail = new(claudeOutputDetail)
			var o seen
			return s.object(func(key []byte) bool {
				if string(key) == "thinking_tokens" {
					return o.first(0) && s.setInt64(&u.OutputDetail.Thinking)
				}
				return !foldsToField(key, claudeOutputFields) && s.value()
			})
		}
		return !foldsToField(key, claudeUsageFields) && s.value()
	})
}

// scan reads a content of one string or of a list of blocks; anything else is left to encoding/json.
func (c *claudeContent) scan(raw []byte) bool {
	s := scanner{b: raw}
	s.ws()
	if s.i < len(raw) && raw[s.i] == '"' {
		c.IsText = true
		return s.setText(&c.Text) && s.done()
	}
	c.Blocks = []claudeBlock{} // an empty list is an empty slice, as encoding/json decodes it
	ok := s.array(func() bool {
		var b claudeBlock
		ok := s.claudeBlock(&b)
		c.Blocks = append(c.Blocks, b)
		return ok
	})
	return ok && s.done()
}

func (s *scanner) claudeBlock(b *claudeBlock) bool {
	var m seen
	var input []byte // the input as written, for the skill of a Skill call
	ok := s.object(func(key []byte) bool {
		switch string(key) {
		case "type":
			return m.first(0) && s.setText(&b.Type)
		case "text":
			return m.first(1) && s.setText(&b.Text)
		case "id":
			return m.first(2) && s.setText(&b.ID)
		case "name":
			return m.first(3) && s.setText(&b.Name)
		case "input":
			if !m.first(4) {
				return false
			}
			start := s.i
			if !s.setSize((*int)(&b.Input)) {
				return false
			}
			input = s.b[start:s.i]
			return true
		case "tool_use_id":
			return m.first(5) && s.setText(&b.ToolUseID)
		case "content":
			return m.first(6) && s.setSize((*int)(&b.Content))
		case "is_error":
			return m.first(7) && s.setBool(&b.IsError)
		}
		return !foldsToField(key, claudeBlockFields) && s.value()
	})
	if ok && b.Name == "Skill" {
		b.Skill = claudeSkill(input)
	}
	return ok
}
