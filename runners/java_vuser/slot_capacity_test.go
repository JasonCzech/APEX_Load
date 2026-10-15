package java_vuser_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPooledHostAdmitsMaximumSlots drives one real JVM through the full
// lifetime admission ceiling: every slot gets a private loader and lifecycle
// thread, initializes against a shared dependency, runs an action and closes.
func TestPooledHostAdmitsMaximumSlots(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java unavailable")
	}
	dir := t.TempDir()
	api := filepath.Join(dir, "api")
	shim := filepath.Join(dir, "shim")
	script := filepath.Join(dir, "script")
	shared := filepath.Join(dir, "shared")
	for _, p := range []string{api, shim, script, shared} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(bin string, args ...string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", bin, err, out)
		}
	}
	for name, source := range map[string]string{"CapacityShared.java": capacitySharedFixture, "CapacityActions.java": capacityActions, "CapacityProbe.java": capacityProbe} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run(javac, "--release", "8", "-d", api, "apexload/bridge/SlotRuntime.java", "apexload/host/SlotLoader.java", "apexload/host/SlotWorker.java", "apexload/host/PooledHost.java")
	run(javac, "--release", "8", "-cp", api, "-d", shim, "lrapi/lr.java", "lrapi/SlotBridge.java")
	run(javac, "--release", "8", "-d", shared, filepath.Join(dir, "CapacityShared.java"))
	run(javac, "--release", "8", "-cp", shim+string(os.PathListSeparator)+shared, "-d", script, filepath.Join(dir, "CapacityActions.java"))
	run(javac, "--release", "8", "-cp", api, "-d", api, filepath.Join(dir, "CapacityProbe.java"))
	run(java, "-Xss256k", "-cp", api, "CapacityProbe", shim, script, shared)
}

const capacitySharedFixture = `
import java.util.concurrent.atomic.AtomicInteger;
public final class CapacityShared { public static final AtomicInteger inits=new AtomicInteger(); }
`

const capacityActions = `
import lrapi.lr;
public final class CapacityActions {
  private static int privateInits;
  public int init(){if(++privateInits!=1)throw new AssertionError("private statics shared");CapacityShared.inits.incrementAndGet();return 0;}
  public int action(){return lr.get_vuser_id()>=0?0:1;}
  public int end(){return 0;}
}`

const capacityProbe = `
import apexload.host.PooledHost;
import apexload.host.SlotWorker;
import java.io.File;
import java.net.URL;
import java.util.*;
import java.util.concurrent.*;
public final class CapacityProbe {
  public static void main(String[] args)throws Exception{
    if(PooledHost.MAX_SLOTS!=1024)throw new AssertionError("ceiling drifted from javarunner.MaxPooledSlotsPerHost");
    URL shim=new File(args[0]).toURI().toURL();
    URL[] scripts={new File(args[1]).toURI().toURL()};
    URL[] shared={new File(args[2]).toURI().toURL()};
    try{new PooledHost("gen-1",PooledHost.MAX_SLOTS+1,shim,scripts,shared,"CapacityActions");throw new AssertionError("over-ceiling host accepted");}
    catch(IllegalArgumentException expected){}
    PooledHost h=new PooledHost("gen-1",PooledHost.MAX_SLOTS,shim,scripts,shared,"CapacityActions");
    Map<String,String> empty=Collections.emptyMap();
    try{
      for(int i=0;i<PooledHost.MAX_SLOTS;i++)h.prepareSlot("gen-1","s"+i,Collections.singletonMap("APEXLOAD_VUSER_ID",Integer.toString(i)));
      try{h.prepareSlot("gen-1","extra",empty);throw new AssertionError("admission past capacity");}catch(IllegalStateException expected){}
      h.commit("gen-1");
      List<CompletableFuture<SlotWorker.Result>> results=new ArrayList<CompletableFuture<SlotWorker.Result>>();
      for(int i=0;i<PooledHost.MAX_SLOTS;i++)results.add(h.invoke("gen-1","s"+i,1,"i"+i,"init",empty));
      for(CompletableFuture<SlotWorker.Result> f:results)if(f.get(60,TimeUnit.SECONDS).returnCode!=0)throw new AssertionError("init failed");
      results.clear();
      for(int i=0;i<PooledHost.MAX_SLOTS;i++)results.add(h.invoke("gen-1","s"+i,2,"a"+i,"action",empty));
      for(CompletableFuture<SlotWorker.Result> f:results)if(f.get(60,TimeUnit.SECONDS).returnCode!=0)throw new AssertionError("action failed");
      List<CompletableFuture<?>> closures=new ArrayList<CompletableFuture<?>>();
      for(int i=0;i<PooledHost.MAX_SLOTS;i++)closures.add(h.closeSlot("gen-1","s"+i,true));
      for(CompletableFuture<?> f:closures)f.get(60,TimeUnit.SECONDS);
      if(h.requiresReap())throw new AssertionError("orderly closure requested reap");
      h.awaitClosed(30000);
    }finally{h.requestReap();h.awaitClosed(5000);}
  }
}`
