package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDOCXTextExtraction(t *testing.T) {
	path:=filepath.Join(t.TempDir(),"sample.docx")
	f,err:=os.Create(path)
	if err!=nil{t.Fatal(err)}
	w:=zip.NewWriter(f)
	part,err:=w.Create("word/document.xml")
	if err!=nil{t.Fatal(err)}
	_,err=part.Write([]byte("<w:document xmlns:w=\""+wordNS+"\"><w:body><w:p><w:r><w:t>Teste &amp; dados</w:t></w:r></w:p></w:body></w:document>"))
	if err!=nil{t.Fatal(err)}
	if err=w.Close();err!=nil{t.Fatal(err)}
	if err=f.Close();err!=nil{t.Fatal(err)}
	got,err:=documentPrompt("Resuma",path)
	if err!=nil||!strings.Contains(got,"Teste & dados"){t.Fatalf("%q %v",got,err)}
}
