package logfiles

import "encoding/json"

// The scanner's reading of a pi or oh-my-pi line: the same piLine encoding/json fills, field for field.
// Keys that are not listed are skipped without being decoded.

var (
	piLineFields    = fieldNames("type", "id", "timestamp", "cwd", "title", "model", "provider", "modelId", "message", "purpose", "duration", "ttft", "stopReason", "errorMessage", "usage", "serviceTier")
	piMessageFields = fieldNames("role", "content", "provider", "model", "stopReason", "errorMessage", "duration", "ttft", "toolCallId", "isError", "usage", "serviceTier")
	piUsageFields   = fieldNames("input", "output", "cacheRead", "cacheWrite", "reasoningTokens", "cost", "premiumRequests")
	piCostFields    = fieldNames("total")
	piBlockFields   = fieldNames("type", "text", "id", "name", "arguments")
)

func scanPiLine(line []byte, l *piLine) bool {
	s := scanner{b: line}
	s.ws()
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "type":
			return m.first(0) && s.setText(&l.Type)
		case "id":
			return m.first(1) && s.setText(&l.ID)
		case "timestamp":
			return m.first(2) && s.setText(&l.Timestamp)
		case "cwd":
			return m.first(3) && s.setText(&l.Cwd)
		case "title":
			return m.first(4) && s.setText(&l.Title)
		case "model":
			return m.first(5) && s.setText(&l.Model)
		case "provider":
			return m.first(6) && s.setText(&l.Provider)
		case "modelId":
			return m.first(7) && s.setText(&l.ModelID)
		case "message":
			if !m.first(8) {
				return false
			}
			l.Message = new(piMessage)
			return s.piMessage(l.Message)
		case "purpose":
			return m.first(9) && s.setText(&l.Purpose)
		case "duration":
			return m.first(10) && s.setFloat64(&l.Duration)
		case "ttft":
			return m.first(11) && s.setFloat64(&l.TTFT)
		case "stopReason":
			return m.first(12) && s.setText(&l.StopReason)
		case "errorMessage":
			return m.first(13) && s.setText(&l.ErrorMessage)
		case "usage":
			return m.first(14) && s.piUsageField(&l.Usage)
		case "serviceTier":
			return m.first(15) && s.setText(&l.ServiceTier)
		}
		return !foldsToField(key, piLineFields) && s.value()
	}) && s.done()
}

func (s *scanner) piMessage(msg *piMessage) bool {
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "role":
			return m.first(0) && s.setText(&msg.Role)
		case "content":
			if !m.first(1) {
				return false
			}
			start := s.i
			n, ok := s.size()
			msg.Content = piContent{Size: n, raw: s.b[start:s.i]}
			return ok
		case "provider":
			return m.first(2) && s.setText(&msg.Provider)
		case "model":
			return m.first(3) && s.setText(&msg.Model)
		case "stopReason":
			return m.first(4) && s.setText(&msg.StopReason)
		case "errorMessage":
			return m.first(5) && s.setText(&msg.ErrorMessage)
		case "duration":
			return m.first(6) && s.setFloat64(&msg.Duration)
		case "ttft":
			return m.first(7) && s.setFloat64(&msg.TTFT)
		case "toolCallId":
			return m.first(8) && s.setText(&msg.ToolCallID)
		case "isError":
			return m.first(9) && s.setBool(&msg.IsError)
		case "usage":
			return m.first(10) && s.piUsageField(&msg.Usage)
		case "serviceTier":
			return m.first(11) && s.setText(&msg.ServiceTier)
		}
		return !foldsToField(key, piMessageFields) && s.value()
	})
}

func (s *scanner) piUsageField(u **piUsageLog) bool {
	*u = new(piUsageLog)
	return s.piUsage(*u)
}

func (s *scanner) piUsage(u *piUsageLog) bool {
	var m seen
	return s.object(func(key []byte) bool {
		switch string(key) {
		case "input":
			return m.first(0) && s.setInt64(&u.Input)
		case "output":
			return m.first(1) && s.setInt64(&u.Output)
		case "cacheRead":
			return m.first(2) && s.setInt64(&u.CacheRead)
		case "cacheWrite":
			return m.first(3) && s.setInt64(&u.CacheWrite)
		case "reasoningTokens":
			return m.first(4) && s.setInt64(&u.Reasoning)
		case "premiumRequests":
			return m.first(6) && s.setFloat64(&u.PremiumUnits)
		case "cost":
			if !m.first(5) {
				return false
			}
			u.Cost = new(piCost)
			var c seen
			return s.object(func(key []byte) bool {
				if string(key) == "total" {
					return c.first(0) && s.setFloat64(&u.Cost.Total)
				}
				return !foldsToField(key, piCostFields) && s.value()
			})
		}
		return !foldsToField(key, piUsageFields) && s.value()
	})
}

// decodePiBlocks decodes the content of a message: what the scanner cannot read as a list of blocks goes
// through encoding/json, which ignores what does not fit.
func decodePiBlocks(raw []byte) []piBlock {
	if fastDecode {
		if blocks, ok := scanPiBlocks(raw); ok {
			return blocks
		}
		scanRefused.Add(1)
	}
	var blocks []piBlock
	_ = json.Unmarshal(raw, &blocks)
	return blocks
}

func scanPiBlocks(raw []byte) ([]piBlock, bool) {
	s := scanner{b: raw}
	s.ws()
	blocks := []piBlock{} // an empty list is an empty slice, as encoding/json decodes it
	ok := s.array(func() bool {
		var b piBlock
		var m seen
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
			case "arguments":
				if !m.first(4) {
					return false
				}
				n, ok := s.size()
				b.Arguments = size(n)
				return ok
			}
			return !foldsToField(key, piBlockFields) && s.value()
		})
		blocks = append(blocks, b)
		return ok
	})
	return blocks, ok && s.done()
}
