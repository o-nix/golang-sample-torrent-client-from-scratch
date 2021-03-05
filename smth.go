package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
)

func main() {
	var filePath = "golang-for-professionals.torrent"

	content, err := os.ReadFile(filePath)

	panicWhenReadError(err)

	value := Bdecode(content)
	fmt.Println(value)
}

func Bdecode(data []byte) interface{} {
	reader := bytes.NewReader(data)
	var topLevelList []interface{}

	for {
		value := readGeneric(reader)

		if value == io.EOF {
			switch len(topLevelList) {
			case 1:
				return topLevelList[0]
			case 0:
				return nil
			default:
				return topLevelList
			}
		} else {
			topLevelList = append(topLevelList, value)
		}
	}
}

func Bencode(data interface{}) []byte {
	buffer := bytes.NewBuffer([]byte{})

	writeGeneric(data, buffer)

	return buffer.Bytes()
}

func writeGeneric(data interface{}, buffer *bytes.Buffer) {
	switch cast := data.(type) {
	case string:
		writeString(cast, buffer)
	case int:
		writeInt(cast, buffer)
	case []interface{}:
		writeList(cast, buffer)
	case map[string]interface{}:
		writeDict(cast, buffer)
	}
}

func writeDict(dict map[string]interface{}, buffer *bytes.Buffer) {
	buffer.WriteString("d")

	for k, v := range dict {
		writeString(k, buffer)
		writeGeneric(v, buffer)
	}

	buffer.WriteString("e")
}

func writeList(list []interface{}, buffer *bytes.Buffer) {
	buffer.WriteString("l")

	for _, v := range list {
		writeGeneric(v, buffer)
	}

	buffer.WriteString("e")
}

func writeInt(number int, buffer *bytes.Buffer) {
	buffer.WriteString("i")
	buffer.WriteString(strconv.Itoa(number))
	buffer.WriteString("e")
}

func writeString(str string, buffer *bytes.Buffer) {
	buffer.WriteString(strconv.Itoa(len(str)))
	buffer.WriteString(":")
	buffer.WriteString(str)
}

func readGeneric(reader *bytes.Reader) interface{} {
	for {
		curr, err := reader.ReadByte()

		if err == io.EOF {
			return io.EOF
		}

		switch {
		case curr == 'd':
			return readDict(reader)
		case isDigit(curr):
			_ = reader.UnreadByte()

			return readString(reader)
		case curr == 'i':
			return readInt(reader)
		case curr == 'l':
			return readList(reader)
		}
	}
}

func isDigit(byte byte) bool {
	return byte >= '0' && byte <= '9'
}

func readString(reader *bytes.Reader) string {
	numberOfNextBytes := readInt(reader)

	buf := make([]byte, numberOfNextBytes, numberOfNextBytes)

	for i := 0; i < numberOfNextBytes; i++ {
		var err error

		buf[i], err = reader.ReadByte()

		panicWhenReadError(err)
	}

	return string(buf)
}

func panicWhenReadError(err error) {
	switch {
	case err == io.EOF:
		panic("Torrent file is corrupted")
	case err != nil:
		panic(fmt.Errorf("unknown read error: %w", err))
	}
}

func readInt(reader *bytes.Reader) int {
	var digits []byte
	var sign = 1

loop:
	for {
		curr, err := reader.ReadByte()

		panicWhenReadError(err)

		switch {
		case curr == '-' && len(digits) == 0:
			sign = -1
		case curr == ':' || curr == 'e':
			break loop
		case isDigit(curr):
			digits = append(digits, curr)
		default:
			panic("Cannot read integer: premature end of data")
		}
	}

	numberOfNextBytes, err := strconv.Atoi(string(digits))

	if err != nil {
		panic("Cannot convert binary string length to value")
	}

	return numberOfNextBytes * sign
}

func readDict(reader *bytes.Reader) map[string]interface{} {
	dict := make(map[string]interface{})

	for {
		key := readString(reader)
		value := readGeneric(reader)

		dict[key] = value

		if isStructEnd(reader) {
			break
		}
	}

	return dict
}

func readList(reader *bytes.Reader) []interface{} {
	var list []interface{}

	for {
		if isStructEnd(reader) {
			return list
		} else {
			value := readGeneric(reader)
			list = append(list, value)
		}
	}
}

func isStructEnd(reader *bytes.Reader) bool {
	readByte, err := reader.ReadByte()

	if err != nil {
		panic("Error detecting end of structure (invalid bencode data?)")
	}

	if readByte == 'e' {
		return true
	} else {
		_ = reader.UnreadByte()

		return false
	}

}
