package rpcmesh

import (
 "os"
 "path/filepath"
 "runtime"
 "strings"
 "testing"
)

func TestNativeRPCEnvironmentIncludesBundledLibraries(t *testing.T) {
 if runtime.GOOS!="linux" {t.Skip("Linux dynamic library lookup")}
 dir:=t.TempDir()
 p:=filepath.Join(dir,"bin","ggml-rpc-server")
 t.Setenv("LD_LIBRARY_PATH","/opt/example")
 got:=nativeRPCEnvironment(p)
 var value string
 for _,entry:=range got {if strings.HasPrefix(entry,"LD_LIBRARY_PATH=") {value=strings.TrimPrefix(entry,"LD_LIBRARY_PATH=")}}
 if value!=filepath.Join(dir,"bin")+string(os.PathListSeparator)+"/opt/example" {
  t.Fatalf("RPC sibling libraries must precede system path: %q",value)
 }
}
