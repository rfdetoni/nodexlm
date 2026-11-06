package hardware
import ("os";"path/filepath";"testing")
func TestDRMGPU(t *testing.T) {
 root:=t.TempDir()
 create:=func(card,vendor,total,used string) {
  t.Helper();dir:=filepath.Join(root,card,"device")
  if err:=os.MkdirAll(dir,0700);err!=nil {t.Fatal(err)}
  for name,value:=range map[string]string{"vendor":vendor,"mem_info_vram_total":total,"mem_info_vram_used":used} {
   if err:=os.WriteFile(filepath.Join(dir,name),[]byte(value),0600);err!=nil {t.Fatal(err)}
  }
 }
 create("card0","0x1002","8589934592","2147483648")
 create("card1","0x8086","0","0")
 create("renderD128","0x1002","100","0")
 got:=drmDevices(root)
 if len(got)!=2 {t.Fatalf("expected two GPUs, got %+v",got)}
 if got[0].Vendor!="amd" || got[0].VRAMBytes!=8<<30 || got[0].FreeVRAMBytes!=6<<30 {t.Fatalf("bad AMD VRAM %+v",got[0])}
 if got[1].Vendor!="intel" || got[1].FreeVRAMBytes!=0 {t.Fatalf("bad Intel GPU %+v",got[1])}
}
func TestDRMUnknownUsedDoesNotInventFreeVRAM(t *testing.T) {
 root:=t.TempDir();dir:=filepath.Join(root,"card2","device")
 if err:=os.MkdirAll(dir,0700);err!=nil {t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(dir,"vendor"),[]byte("0x1002"),0600);err!=nil {t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(dir,"mem_info_vram_total"),[]byte("1024"),0600);err!=nil {t.Fatal(err)}
 got:=drmDevices(root)
 if len(got)!=1 || got[0].VRAMBytes!=1024 || got[0].FreeVRAMBytes!=0 {t.Fatalf("unexpected GPU %+v",got)}
}
