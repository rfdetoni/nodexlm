package main

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestEncodeVisionData(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b,image.NewRGBA(image.Rect(0,0,2,2)));err!=nil{t.Fatal(err)}
	url,err:=encodeVisionData(b.Bytes())
	if err!=nil||!strings.HasPrefix(url,"data:image/png;base64,"){t.Fatalf("%s %v",url,err)}
	if _,err:=encodeVisionData([]byte("not image"));err==nil{t.Fatal("invalid image accepted")}
}
