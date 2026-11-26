package main

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"strings"
)

const maxDOCXArchive = 16 << 20
const maxDOCXXML = 2 << 20
const wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// Extract only document.xml, no external relationships, macro execution,
// images or filesystem extraction. Limit archive, XML and resulting text.
func readDOCX(path string) (string,error) {
	info,err:=os.Stat(path)
	if err!=nil{return "",err}
	if !info.Mode().IsRegular()||info.Size()>maxDOCXArchive{return "",errors.New("DOCX must be a regular file of at most 16 MiB")}
	f,err:=os.Open(path)
	if err!=nil{return "",err}
	defer f.Close()
	opened,err:=f.Stat()
	if err!=nil{return "",err}
	if !os.SameFile(info,opened)||!opened.Mode().IsRegular()||opened.Size()>maxDOCXArchive{return "",errors.New("DOCX changed during opening")}
	z,err:=zip.NewReader(f,opened.Size())
	if err!=nil{return "",err}
	var entry *zip.File
	for _,v:=range z.File {
		if v.Name=="word/document.xml" {
			if entry!=nil{return "",errors.New("duplicate DOCX document part")}
			entry=v
		}
	}
	if entry==nil||entry.UncompressedSize64>maxDOCXXML{return "",errors.New("missing or oversized DOCX document part")}
	r,err:=entry.Open()
	if err!=nil{return "",err}
	defer r.Close()
	limited:=&io.LimitedReader{R:r,N:maxDOCXXML+1}
	decoder:=xml.NewDecoder(limited)
	var out strings.Builder
	depth,textDepth:=0,0
	for {
		token,err:=decoder.Token()
		if err==io.EOF{break}
		if err!=nil{return "",err}
		switch v:=token.(type) {
		case xml.StartElement:
			depth++
			if depth>128{return "",errors.New("DOCX XML too deeply nested")}
			if v.Name.Space==wordNS {
				switch v.Name.Local {
				case "t": textDepth=depth
				case "tab":out.WriteByte('\t')
				case "br","cr":out.WriteByte('\n')
				}
			}
		case xml.CharData:
			if textDepth>0{out.Write(v)}
		case xml.EndElement:
			if depth==textDepth{textDepth=0}
			if v.Name.Space==wordNS&&v.Name.Local=="p"{out.WriteByte('\n')}
			depth--
		}
		if out.Len()>maxChatDocument{return "",errors.New("DOCX extracted text exceeds 64 KiB")}
	}
	if limited.N<=0||depth!=0{return "",errors.New("oversized or incomplete DOCX XML")}
	return out.String(),nil
}
