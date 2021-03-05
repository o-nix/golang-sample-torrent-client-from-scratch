package main

import (
	"reflect"
	"testing"
)

const exampleBencodedString = "l5:helloi-42ed3:key5:valuee2:\u0001\u0002e"

func TestReadBencode(t *testing.T) {
	result := Bdecode([]byte(exampleBencodedString))

	topmostList, successCast := result.([]interface{})

	if !successCast {
		t.Fatal("Topmost structure is not a list")
	}

	asString, successCast := topmostList[0].(string)

	if !successCast || asString != "hello" {
		t.Fatal("Fist element is not a string")
	}

	asSignedInt, successCast := topmostList[1].(int)

	if !successCast || asSignedInt != -42 {
		t.Fatal("Second element should be int")
	}

	asDict, successCast := topmostList[2].(map[string]interface{})

	if !successCast || reflect.DeepEqual(asDict, map[string]string{"key": "value"}) {
		t.Fatal("Third element is not a good dict")
	}

	asByteString, successCast := topmostList[3].(string)

	if !successCast || asByteString[0] != '\x01' || asByteString[1] != '\x02' {
		t.Fatal("Third element is not a good dict")
	}
}

func TestWriteBencode(t *testing.T) {
	binaryResult := Bencode(Bdecode([]byte(exampleBencodedString)))

	if string(binaryResult) != exampleBencodedString {
		t.Fatal("Cannot encode the same way")
	}
}
