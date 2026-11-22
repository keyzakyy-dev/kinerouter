package translate

// Minimal protobuf wire codec for Cursor's ConnectRPC endpoints. Ported from
// open-sse/utils/cursorProtobuf.js (encodeVarint/encodeField/decodeMessage and
// the google.protobuf.Value helpers). Only the wire types Cursor uses are
// implemented: varint, length-delimited and fixed64 (double).

import (
	"encoding/binary"
	"math"
	"sort"
)

const (
	cursorWireVarint  = 0
	cursorWireFixed64 = 1
	cursorWireLen     = 2
)

// pbVarint encodes an unsigned varint.
func pbVarint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v&0x7f)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

// pbBytes encodes one length-delimited field.
func pbBytes(field int, data []byte) []byte {
	out := append(pbVarint(uint64(field<<3|cursorWireLen)), pbVarint(uint64(len(data)))...)
	return append(out, data...)
}

// pbString encodes one length-delimited string field.
func pbString(field int, s string) []byte { return pbBytes(field, []byte(s)) }

// pbUint encodes one varint field.
func pbUint(field int, v uint64) []byte {
	return append(pbVarint(uint64(field<<3|cursorWireVarint)), pbVarint(v)...)
}

// pbDouble encodes one fixed64 (little-endian double) field.
func pbDouble(field int, v float64) []byte {
	out := pbVarint(uint64(field<<3 | cursorWireFixed64))
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
	return append(out, buf[:]...)
}

// pbConcat joins encoded fields.
func pbConcat(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// pbField is one decoded field occurrence.
type pbField struct {
	wire  int
	num   uint64 // varint value (wire 0)
	bytes []byte // raw bytes (wire 2), 8 raw bytes (wire 1)
}

// pbDecode splits a message into its fields. Malformed input truncates like the
// JS decoder instead of erroring: a partially updated Cursor schema must degrade
// to "no content", never crash the stream.
func pbDecode(data []byte) map[int][]pbField {
	fields := map[int][]pbField{}
	pos := 0
	for pos < len(data) {
		tag, n := pbReadVarint(data, pos)
		if n == 0 {
			break
		}
		pos = n
		num, wire := int(tag>>3), int(tag&7)
		switch wire {
		case cursorWireVarint:
			v, n := pbReadVarint(data, pos)
			if n == 0 {
				return fields
			}
			pos = n
			fields[num] = append(fields[num], pbField{wire: wire, num: v})
		case cursorWireLen:
			l, n := pbReadVarint(data, pos)
			if n == 0 || int(l) < 0 || n+int(l) > len(data) {
				return fields
			}
			fields[num] = append(fields[num], pbField{wire: wire, bytes: data[n : n+int(l)]})
			pos = n + int(l)
		case cursorWireFixed64:
			if pos+8 > len(data) {
				return fields
			}
			fields[num] = append(fields[num], pbField{wire: wire, bytes: data[pos : pos+8]})
			pos += 8
		default:
			return fields
		}
	}
	return fields
}

// pbReadVarint decodes one varint, returning the value and the next offset (0 on
// truncation).
func pbReadVarint(data []byte, pos int) (uint64, int) {
	var v uint64
	var shift uint
	for pos < len(data) {
		b := data[pos]
		v |= uint64(b&0x7f) << shift
		pos++
		if b&0x80 == 0 {
			return v, pos
		}
		shift += 7
		if shift > 63 {
			return 0, 0
		}
	}
	return 0, 0
}

// pbMessage returns the first length-delimited field as a decoded message.
func pbMessage(f map[int][]pbField, num int) (map[int][]pbField, bool) {
	l := f[num]
	if len(l) == 0 || l[0].wire != cursorWireLen {
		return nil, false
	}
	return pbDecode(l[0].bytes), true
}

// pbStringField returns the first length-delimited field as a string.
func pbStringField(f map[int][]pbField, num int) string {
	l := f[num]
	if len(l) == 0 || l[0].wire != cursorWireLen {
		return ""
	}
	return string(l[0].bytes)
}

// pbUintField returns the first varint field.
func pbUintField(f map[int][]pbField, num int) uint64 {
	l := f[num]
	if len(l) == 0 || l[0].wire != cursorWireVarint {
		return 0
	}
	return l[0].num
}

// ------------------------------------------------- google.protobuf.Value

const (
	pbValueNull   = 1
	pbValueNumber = 2
	pbValueString = 3
	pbValueBool   = 4
	pbValueStruct = 5
	pbValueList   = 6
)

// pbEncodeValue encodes a decoded JSON value as google.protobuf.Value.
func pbEncodeValue(v any) []byte {
	switch t := v.(type) {
	case nil:
		return pbUint(pbValueNull, 0)
	case bool:
		n := uint64(0)
		if t {
			n = 1
		}
		return pbUint(pbValueBool, n)
	case float64:
		return pbDouble(pbValueNumber, t)
	case int:
		return pbDouble(pbValueNumber, float64(t))
	case int64:
		return pbDouble(pbValueNumber, float64(t))
	case string:
		return pbString(pbValueString, t)
	case []any:
		var items []byte
		for _, item := range t {
			items = append(items, pbBytes(1, pbEncodeValue(item))...)
		}
		return pbBytes(pbValueList, items)
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var entries []byte
		for _, k := range keys {
			entries = append(entries, pbBytes(1, pbConcat(pbString(1, k), pbBytes(2, pbEncodeValue(t[k]))))...)
		}
		return pbBytes(pbValueStruct, entries)
	}
	return pbString(pbValueString, "")
}

// pbDecodeValue decodes a google.protobuf.Value into a plain Go value.
func pbDecodeValue(data []byte) any {
	f := pbDecode(data)
	switch {
	case len(f[pbValueNull]) > 0:
		return nil
	case len(f[pbValueBool]) > 0:
		return pbUintField(f, pbValueBool) != 0
	case len(f[pbValueNumber]) > 0:
		if b := f[pbValueNumber][0].bytes; len(b) == 8 {
			return math.Float64frombits(binary.LittleEndian.Uint64(b))
		}
		return float64(0)
	case len(f[pbValueString]) > 0:
		return pbStringField(f, pbValueString)
	case len(f[pbValueStruct]) > 0:
		out := map[string]any{}
		for _, entry := range f[pbValueStruct][0].Message()[1] {
			pair := pbDecode(entry.bytes)
			key := pbStringField(pair, 1)
			if key != "" {
				out[key] = pbDecodeValue(pbStringFieldBytes(pair, 2))
			}
		}
		return out
	case len(f[pbValueList]) > 0:
		var out []any
		for _, item := range f[pbValueList][0].Message()[1] {
			out = append(out, pbDecodeValue(item.bytes))
		}
		return out
	}
	return nil
}

// Message returns this field's bytes decoded as a message (helper for the two
// nested loops above).
func (f pbField) Message() map[int][]pbField { return pbDecode(f.bytes) }

func pbStringFieldBytes(f map[int][]pbField, num int) []byte {
	l := f[num]
	if len(l) == 0 {
		return nil
	}
	return l[0].bytes
}
