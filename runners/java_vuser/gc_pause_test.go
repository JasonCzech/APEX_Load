package java_vuser_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGCPauseCaptureJava8BuildAndBoundedRuntime(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "GCPauseCheck.java")
	code := `package apexload.host;
import java.util.*;
public class GCPauseCheck {
 static volatile byte[] allocated;
 public static void main(String[] args) throws Exception {
  GCPauses capture=new GCPauses();
  for(int i=0;i<130;i++) capture.record(1000+i,10);
  Map<String,String> fields=new HashMap<String,String>();capture.fields(fields);
  if(!"2".equals(fields.get("gc_pause_evicted")) || !fields.get("gc_pause_events").startsWith("3,1002,10\n") || fields.get("gc_pause_events").split("\n").length!=128) throw new AssertionError(fields);
  capture.record(Long.MAX_VALUE,1);capture.fields(fields);
  if(!"failed".equals(fields.get("gc_pause_status")))throw new AssertionError(fields);
  GCPauses live=GCPauses.start();live.fields(fields);
  String vm=System.getProperty("java.vm.name", "");
  String version=System.getProperty("java.specification.version", "8");
  boolean supported=!version.startsWith("1.") && Integer.parseInt(version)>=17 && (vm.contains("HotSpot") || vm.contains("OpenJDK"));
  if(supported && !"available".equals(fields.get("gc_pause_status")))throw new AssertionError(fields);
  if(supported) {
   for(int i=0;i<80;i++) allocated=new byte[1024*1024];
   long deadline=System.nanoTime()+5000000000L;
   do {Thread.sleep(20);live.fields(fields);} while(fields.get("gc_pause_events").isEmpty() && System.nanoTime()<deadline);
   if(fields.get("gc_pause_events").isEmpty())throw new AssertionError("No platform GC pause observed");
  }
  live.close();
 }
}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, javac, "--release", "8", "-d", dir, "apexload/host/GCPauses.java", source).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v %s", err, out)
	}
	if out, err := exec.CommandContext(ctx, java, "-Xmx64m", "-XX:+IgnoreUnrecognizedVMOptions", "-XX:FlightRecorderOptions=repository="+dir, "-cp", dir, "apexload.host.GCPauseCheck").CombinedOutput(); err != nil {
		t.Fatalf("JFR runtime: %v %s", err, out)
	}
}
