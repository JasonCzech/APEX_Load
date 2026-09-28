package java_vuser_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPrivateSlotBridgeAndContext(t *testing.T) {
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
	for _, p := range []string{api, shim, script} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(bin string, args ...string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "APEXLOAD_SCENARIO_ID=host-only")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", bin, err, out)
		}
	}
	run(javac, "--release", "8", "-d", api, "apexload/bridge/SlotRuntime.java", "apexload/host/SlotLoader.java", "apexload/host/SlotWorker.java", "apexload/host/PooledHost.java")
	run(javac, "--release", "8", "-cp", api, "-d", shim, "lrapi/lr.java", "lrapi/SlotBridge.java")
	probe := filepath.Join(dir, "SlotProbe.java")
	if err := os.WriteFile(probe, []byte(slotProbe), 0600); err != nil {
		t.Fatal(err)
	}
	run(javac, "--release", "8", "-cp", api, "-d", api, probe)
	run(java, "-cp", api, "SlotProbe", shim, script, dir)
	workerProbe := filepath.Join(dir, "WorkerProbe.java")
	workerActions := filepath.Join(dir, "WorkerActions.java")
	for path, source := range map[string]string{workerProbe: slotWorkerProbe, workerActions: slotWorkerActions} {
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run(javac, "--release", "8", "-cp", shim, "-d", script, workerActions)
	run(javac, "--release", "8", "-cp", api, "-d", api, workerProbe)
	run(java, "-cp", api, "WorkerProbe", shim, script, dir)
	legacy := filepath.Join(dir, "legacy-shim")
	if err := os.MkdirAll(filepath.Join(legacy, "lrapi"), 0700); err != nil {
		t.Fatal(err)
	}
	legacySource := filepath.Join(legacy, "lrapi", "SlotBridge.java")
	if err := os.WriteFile(legacySource, []byte(incompatibleSlotBridge), 0600); err != nil {
		t.Fatal(err)
	}
	compatibilityProbe := filepath.Join(dir, "CompatibilityProbe.java")
	if err := os.WriteFile(compatibilityProbe, []byte(slotCompatibilityProbe), 0600); err != nil {
		t.Fatal(err)
	}
	run(javac, "--release", "8", "-cp", api, "-d", legacy, legacySource)
	run(javac, "--release", "8", "-cp", api, "-d", api, compatibilityProbe)
	run(java, "-cp", api, "CompatibilityProbe", legacy, script, filepath.Join(dir, "incompatible-marker"))
	shared := filepath.Join(dir, "shared")
	if err := os.MkdirAll(shared, 0700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"SharedFixture.java": hostSharedFixture, "HostActions.java": hostActions, "HostProbe.java": pooledHostProbe} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run(javac, "--release", "8", "-d", shared, filepath.Join(dir, "SharedFixture.java"))
	run(javac, "--release", "8", "-cp", shim+string(os.PathListSeparator)+shared, "-d", script, filepath.Join(dir, "HostActions.java"))
	run(javac, "--release", "8", "-cp", api, "-d", api, filepath.Join(dir, "HostProbe.java"))
	run(java, "-cp", api, "HostProbe", shim, script, shared, dir)
}

const hostSharedFixture = `
import java.util.concurrent.atomic.AtomicInteger;
public final class SharedFixture { public static final AtomicInteger count=new AtomicInteger(); }
`
const hostActions = `
import lrapi.lr;
public final class HostActions {
  private static int privateCount;
  private final Thread owner=Thread.currentThread();
  private int identity;
  public int init(){if(lr.eval_string("{mode}").equals("fail-init"))return 1;identity=lr.get_vuser_id();SharedFixture.count.incrementAndGet();if(++privateCount!=1)throw new AssertionError("private statics");return 0;}
  public int action() throws Exception {
    if(Thread.currentThread()!=owner||lr.get_vuser_id()!=identity||privateCount!=1)throw new AssertionError("private state leaked");
    if(lr.eval_string("{mode}").equals("fail"))throw new IllegalStateException("triggering failure");
    if(lr.eval_string("{mode}").equals("block")) {
      java.nio.file.Files.write(java.nio.file.Paths.get(lr.eval_string("{marker}")),new byte[]{1});
      Thread.sleep(30000);
    }
    if(SharedFixture.count.get()!=2)throw new AssertionError("dependencies not shared");return 0;
  }
  public int end(){return 0;}
}`
const pooledHostProbe = `
import apexload.host.PooledHost;
import java.io.File;
import java.net.URL;
import java.util.*;
import java.util.concurrent.*;
public final class HostProbe {
  static PooledHost host(String[] args)throws Exception{
    return new PooledHost("gen-1",2,new File(args[0]).toURI().toURL(),
      new URL[]{new File(args[1]).toURI().toURL()},new URL[]{new File(args[2]).toURI().toURL()},"HostActions");
  }
  public static void main(String[] args)throws Exception{
    Map<String,String> empty=Collections.emptyMap();
    PooledHost h=host(args);
    try{
      h.prepareSlot("gen-1","slot-a",Collections.singletonMap("APEXLOAD_VUSER_ID","17"));
      h.prepareSlot("gen-1","slot-b",Collections.singletonMap("APEXLOAD_VUSER_ID","18"));
      try{h.invoke("gen-1","slot-a",1,"a1","init",empty);throw new AssertionError("precommit");}catch(IllegalStateException expected){}
      try{h.commit("old");throw new AssertionError("stale generation");}catch(IllegalStateException expected){}
      h.commit("gen-1");
      if(h.invoke("gen-1","slot-a",1,"a1","init",empty).get(5,TimeUnit.SECONDS).returnCode!=0
        ||h.invoke("gen-1","slot-b",1,"b1","init",empty).get(5,TimeUnit.SECONDS).returnCode!=0)throw new AssertionError("private statics");
      if(h.invoke("gen-1","slot-a",2,"a2","action",empty).get(5,TimeUnit.SECONDS).returnCode!=0)throw new AssertionError("dependencies were not shared");
      h.closeSlot("gen-1","slot-a",true).get(5,TimeUnit.SECONDS);
      if(!h.isSealed()||h.requiresReap())throw new AssertionError("cooperative close");
      try{h.prepareSlot("gen-1","replacement",empty);throw new AssertionError("sealed admission");}catch(IllegalStateException expected){}
      try{h.awaitClosed(10);throw new AssertionError("live peer reaped");}catch(IllegalStateException expected){}
      if(h.invoke("gen-1","slot-b",2,"b2","action",empty).get(5,TimeUnit.SECONDS).returnCode!=0)throw new AssertionError("survivor changed");
      h.closeSlot("gen-1","slot-b",true).get(5,TimeUnit.SECONDS);h.awaitClosed(5000);
    }finally{h.requestReap();h.awaitClosed(5000);}
    PooledHost failed=host(args);
    try{
      failed.prepareSlot("gen-1","a",empty);failed.prepareSlot("gen-1","b",empty);failed.commit("gen-1");
      failed.invoke("gen-1","a",1,"a1","init",empty).get(5,TimeUnit.SECONDS);
      failed.invoke("gen-1","b",1,"b1","init",empty).get(5,TimeUnit.SECONDS);
      try{failed.invoke("gen-1","a",2,"a2","action",Collections.singletonMap("mode","fail")).get(5,TimeUnit.SECONDS);throw new AssertionError("failure lost");}
      catch(ExecutionException expected){if(!expected.getCause().getMessage().equals("triggering failure"))throw expected;}
      if(!failed.requiresReap())throw new AssertionError("failed host reusable");
      try{failed.invoke("gen-1","b",2,"b2","action",empty);throw new AssertionError("peer accepted after reap");}catch(IllegalStateException expected){}
    }finally{failed.requestReap();failed.awaitClosed(5000);}
    PooledHost failedInit=host(args);
    try{
      failedInit.prepareSlot("gen-1","a",empty);failedInit.commit("gen-1");
      if(failedInit.invoke("gen-1","a",1,"init-fail","init",Collections.singletonMap("mode","fail-init")).get(5,TimeUnit.SECONDS).returnCode!=1)
        throw new AssertionError("init failure lost");
      if(!failedInit.requiresReap())throw new AssertionError("failed init reusable");
      try{failedInit.invoke("gen-1","a",2,"action","action",empty);throw new AssertionError("action after failed init");}catch(IllegalStateException expected){}
    }finally{failedInit.requestReap();failedInit.awaitClosed(5000);}
    PooledHost cancelled=host(args);
    try{
      cancelled.prepareSlot("gen-1","a",empty);cancelled.prepareSlot("gen-1","b",empty);cancelled.commit("gen-1");
      cancelled.invoke("gen-1","a",1,"a1","init",empty).get(5,TimeUnit.SECONDS);
      cancelled.invoke("gen-1","b",1,"b1","init",empty).get(5,TimeUnit.SECONDS);
      Map<String,String> params=new HashMap<String,String>();params.put("mode","block");params.put("marker",args[3]+"/host-blocked");
      CompletableFuture<?> invocation=cancelled.invoke("gen-1","a",2,"a2","action",params);
      long deadline=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
      while(!new File(params.get("marker")).exists()&&System.nanoTime()<deadline)Thread.sleep(10);
      if(!new File(params.get("marker")).exists())throw new AssertionError("action never entered");
      cancelled.closeSlot("gen-1","a",true).get(2,TimeUnit.SECONDS);
      try{invocation.get(1,TimeUnit.SECONDS);throw new AssertionError("interruption missing");}catch(ExecutionException expected){}
      if(cancelled.requiresReap())throw new AssertionError("cooperative cancellation reaped peer");
      cancelled.invoke("gen-1","b",2,"b2","action",empty).get(5,TimeUnit.SECONDS);
      cancelled.closeSlot("gen-1","b",true).get(5,TimeUnit.SECONDS);cancelled.awaitClosed(5000);
    }finally{cancelled.requestReap();cancelled.awaitClosed(5000);}
  }
}`

const slotWorkerActions = `
import lrapi.lr;
import java.nio.file.*;
public final class WorkerActions {
  private static final Thread staticThread = Thread.currentThread();
  private static final String marker = lr.eval_string("{marker}");
  static {
    if(marker.equals("{marker}")) throw new AssertionError("unbound static context");
    try {Files.write(Paths.get(marker), "initialized".getBytes("UTF-8"));}
    catch(Exception e){throw new RuntimeException(e);}
  }
  private final Thread constructionThread = Thread.currentThread();
  public WorkerActions() {check();}
  private void check() {
    if(Thread.currentThread()!=constructionThread || Thread.currentThread()!=staticThread
       || Thread.currentThread().getContextClassLoader()!=getClass().getClassLoader())
      throw new AssertionError("lifecycle thread changed");
  }
  public int init() {check();if(lr.get_vuser_id()!=20&&lr.get_vuser_id()!=21)throw new AssertionError("identity");return 0;}
  public int action() throws Exception {
    check();
    String mode = lr.eval_string("{mode}");
    if(mode.equals("exit")) {lr.exit(lr.EXIT_ITERATION_AND_CONTINUE,lr.FAIL);}
    if(mode.equals("block")) {
      Files.write(Paths.get(marker+".busy"),new byte[]{1});
      Thread.sleep(30000);
    }
    return lr.get_vuser_id();
  }
  public int end() {check();return 0;}
}`

const slotWorkerProbe = `
import apexload.host.SlotWorker;
import java.io.File;
import java.net.URL;
import java.util.*;
import java.util.concurrent.*;
public final class WorkerProbe {
  public static final class SharedTarget {
    static { System.setProperty("apexload.shared.target.initialized","yes"); }
    public int init() { return 0; }
  }
  static Map<String,String> params(String marker,String mode) {
    Map<String,String> p=new HashMap<String,String>();p.put("marker",marker);p.put("mode",mode);return p;
  }
  static SlotWorker create(String id,String[] args) throws Exception {
    Map<String,String> c=new HashMap<String,String>();c.put("APEXLOAD_VUSER_ID",id);
    c.put("APEXLOAD_JAVA_VUSER_EVENT_FILE",args[2]+"/worker-"+id+".events");
    return new SlotWorker(id,new File(args[0]).toURI().toURL(),
      new URL[]{new File(args[1]).toURI().toURL()},WorkerProbe.class.getClassLoader(),"WorkerActions",c);
  }
  public static void main(String[] args) throws Exception {
    String marker=args[2]+"/user-code";
    SlotWorker aborted=create("19",args);
    try {aborted.invoke(1,"r1","init",params(marker,""));throw new AssertionError("precommit invocation");}
    catch(IllegalStateException expected){}
    aborted.close();aborted.stopped().get(5,TimeUnit.SECONDS);
    try {aborted.commit();throw new AssertionError("aborted commit");}catch(IllegalStateException expected){}
    if(new File(marker).exists())throw new AssertionError("prepare ran user code");
    SlotWorker sharedTarget=new SlotWorker("shared",new File(args[0]).toURI().toURL(),
      new URL[]{new File(args[1]).toURI().toURL()},WorkerProbe.class.getClassLoader(),
      "WorkerProbe$SharedTarget",Collections.<String,String>emptyMap());
    sharedTarget.commit();
    try {sharedTarget.invoke(1,"s1","init",params(marker,"")).get(5,TimeUnit.SECONDS);throw new AssertionError("shared target accepted");}
    catch(ExecutionException expected){}
    sharedTarget.stopped().get(5,TimeUnit.SECONDS);
    if(System.getProperty("apexload.shared.target.initialized")!=null)throw new AssertionError("shared target initialized");
    SlotWorker a=create("20",args),b=create("21",args);
    try {
      a.commit();b.commit();
      if(new File(marker).exists())throw new AssertionError("commit ran user code");
      if(a.invoke(1,"a1","init",params(marker,"")).get(5,TimeUnit.SECONDS).returnCode!=0
        ||b.invoke(1,"b1","init",params(marker+"-b","")).get(5,TimeUnit.SECONDS).returnCode!=0)
        throw new AssertionError("private context or initialization");
      try {a.invoke(1,"a1","action",params(marker,""));throw new AssertionError("replay");}
      catch(IllegalStateException expected){}
      SlotWorker.Result exit=a.invoke(2,"a2","action",params(marker,"exit")).get(5,TimeUnit.SECONDS);
      if(exit.exit==null||exit.exit.option!=2||exit.exit.status!=1||exit.returnCode!=0)
        throw new AssertionError("exit contract lost");
      CompletableFuture<SlotWorker.Result> blocked=a.invoke(3,"a3","action",params(marker,"block"));
      long deadline=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
      while(!new File(marker+".busy").exists()&&System.nanoTime()<deadline)Thread.sleep(10);
      if(!new File(marker+".busy").exists())throw new AssertionError("action did not begin");
      try {a.invoke(4,"a4","action",params(marker,""));throw new AssertionError("busy admission");}
      catch(IllegalStateException expected){}
      if(a.state()!=SlotWorker.State.BUSY || blocked.isDone())throw new AssertionError("busy slot released");
      if(b.invoke(2,"b2","action",params(marker+"-b","")).get(5,TimeUnit.SECONDS).returnCode!=21)
        throw new AssertionError("peer was blocked");
      a.close();a.stopped().get(2,TimeUnit.SECONDS);
      try {blocked.get(1,TimeUnit.SECONDS);throw new AssertionError("interrupt was lost");}
      catch(ExecutionException expected){}
      try {a.invoke(4,"a4","action",params(marker,""));throw new AssertionError("closed reuse");}
      catch(IllegalStateException expected){}
      b.invoke(3,"b3","end",params(marker+"-b","")).get(5,TimeUnit.SECONDS);
      try {b.invoke(4,"b4","action",params(marker+"-b",""));throw new AssertionError("after end");}
      catch(IllegalStateException expected){}
    } finally {a.close();b.close();a.stopped().get(5,TimeUnit.SECONDS);b.stopped().get(5,TimeUnit.SECONDS);}
  }
}`

const slotProbe = `
import apexload.bridge.SlotRuntime;
import apexload.host.SlotLoader;
import java.net.URL;
import java.io.File;
import java.nio.file.Files;
import java.util.*;
public final class SlotProbe {
  public static void main(String[] args) throws Exception {
    URL shim = new File(args[0]).toURI().toURL();
    URL[] script = new URL[]{new File(args[1]).toURI().toURL()};
    try (SlotLoader a = new SlotLoader(shim, script, SlotProbe.class.getClassLoader());
         SlotLoader b = new SlotLoader(shim, script, SlotProbe.class.getClassLoader())) {
      Class<?> la = a.loadClass("lrapi.lr"), lb = b.loadClass("lrapi.lr");
      if (la == lb || la.getClassLoader() != a || lb.getClassLoader() != b) throw new AssertionError("shim shared");
      SlotRuntime ra = (SlotRuntime)a.loadClass("lrapi.SlotBridge").newInstance();
      SlotRuntime rb = (SlotRuntime)b.loadClass("lrapi.SlotBridge").newInstance();
      Map<String,String> ca = new HashMap<String,String>(); ca.put("APEXLOAD_VUSER_ID","17");
      ca.put("APEXLOAD_GROUP_NAME","one");ca.put("APEXLOAD_JAVA_VUSER_EVENT_FILE",args[2]+"/a.events");
      Map<String,String> cb = new HashMap<String,String>(); cb.put("APEXLOAD_VUSER_ID","18");
      cb.put("APEXLOAD_GROUP_NAME","two");cb.put("APEXLOAD_JAVA_VUSER_EVENT_FILE",args[2]+"/b.events");
      System.setProperty("apexload.java_vuser.event_file",args[2]+"/wrong.events");
      ra.bind(ca);rb.bind(cb);ca.put("APEXLOAD_VUSER_ID","99");
      if (!la.getMethod("get_vuser_id",Object[].class).invoke(null,(Object)new Object[0]).equals(17)) throw new AssertionError("identity");
      if (!lb.getMethod("get_scenario_id",Object[].class).invoke(null,(Object)new Object[0]).equals("scenario")) throw new AssertionError("inherited host context");
      ra.beginInvocation("a",Collections.singletonMap("token","alice"));
      rb.beginInvocation("b",Collections.singletonMap("token","bob"));
      if (!la.getMethod("eval_string",String.class).invoke(null,"{token}").equals("alice")) throw new AssertionError("parameters");
      la.getMethod("start_transaction",String.class).invoke(null,"only-a");
      ra.endInvocation();rb.endInvocation();
      if (new File(args[2]+"/wrong.events").exists()) throw new AssertionError("event property overrode slot");
      if (!new String(Files.readAllBytes(new File(args[2]+"/a.events").toPath()),"UTF-8").contains("only-a")) throw new AssertionError("event missing");
      ra.beginInvocation("next-request",Collections.<String,String>emptyMap());
      final Class<?> backgroundLR = la;
      Thread child = new Thread(new Runnable(){public void run(){
        try {backgroundLR.getMethod("output_message",Object[].class).invoke(null,(Object)new Object[]{"unattributed-background"});}
        catch(Exception e){throw new RuntimeException(e);}
      }});
      child.start();child.join();
      try {ra.endInvocation();throw new AssertionError("background event accepted");}
      catch(IllegalStateException expected){}
      if(new String(Files.readAllBytes(new File(args[2]+"/a.events").toPath()),"UTF-8").contains("unattributed-background"))throw new AssertionError("background event attributed");
      try {ra.beginInvocation("after-failure",Collections.<String,String>emptyMap());throw new AssertionError("attribution failure reset");}
      catch(IllegalStateException expected){}
      long before = new File(args[2]+"/a.events").length();
      la.getMethod("output_message",Object[].class).invoke(null,(Object)new Object[]{"late-output"});
      if(new File(args[2]+"/a.events").length()!=before)throw new AssertionError("late event attributed");
      try {la.getMethod("exit",int.class,int.class).invoke(null,1,1); throw new AssertionError("exit returned");}
      catch(java.lang.reflect.InvocationTargetException e) {
        SlotRuntime.ExitRequest x = ra.exitRequest(e.getCause());
        if(x == null || x.option!=1 || x.status!=1 || rb.exitRequest(e.getCause())!=null) throw new AssertionError("exit bridge");
      }
      try {ra.bind(ca);throw new AssertionError("rebind allowed");}catch(IllegalStateException expected){}
      if(a.loadClass("apexload.bridge.SlotRuntime")!=SlotRuntime.class)throw new AssertionError("bridge duplicated");
    }
  }
}`

const incompatibleSlotBridge = `
package lrapi;
import apexload.bridge.SlotRuntime;
import java.util.Map;
public final class SlotBridge implements SlotRuntime {
 public String eventAttributionContract(){return "legacy";}
 public void bind(Map<String,String> c){throw new AssertionError("unsupported bridge bound");}
 public void beginInvocation(String id,Map<String,String> p){}
 public void endInvocation(){}
 public ExitRequest exitRequest(Throwable t){return null;}
 public void handleExit(int o,int s){}
 public boolean eventFallbackUsed(){return false;}
 public boolean eventCaptureTruncated(){return false;}
}`
const slotCompatibilityProbe = `
import apexload.host.SlotWorker;
import java.io.File;
import java.net.URL;
import java.util.*;
import java.util.concurrent.*;
public final class CompatibilityProbe {
 public static void main(String[] a)throws Exception {
  SlotWorker w=new SlotWorker("compatibility-slot",new File(a[0]).toURI().toURL(),new URL[]{new File(a[1]).toURI().toURL()},CompatibilityProbe.class.getClassLoader(),"WorkerActions",Collections.<String,String>emptyMap());
  try {
   w.commit();
   try {w.invoke(1,"request","init",Collections.singletonMap("marker",a[2])).get(5,TimeUnit.SECONDS);throw new AssertionError("legacy shim accepted");}
   catch(ExecutionException expected) {if(!"pooled_event_attribution_unsupported".equals(expected.getCause().getMessage()))throw expected;}
   if(new File(a[2]).exists())throw new AssertionError("user initialized before bridge validation");
  } finally {w.close();w.stopped().get(5,TimeUnit.SECONDS);}
 }
}`
