package java_vuser_test

import (
	"archive/zip"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Real class loading, resource and service-discovery qualification fixtures.
// Success describes these layouts only; arbitrary dependencies remain subject
// to representative execution and operator approval.
func TestPooledSharedLoaderCompatibility(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java unavailable")
	}
	root := t.TempDir()
	run := func(bin string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if out, e := exec.CommandContext(ctx, bin, args...).CombinedOutput(); e != nil {
			t.Fatalf("Java loader fixture: %v %s", e, out)
		}
	}
	write := func(path, body string) string {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	api, shared, private, extra, shim := filepath.Join(root, "api"), filepath.Join(root, "shared"), filepath.Join(root, "private"), filepath.Join(root, "extra"), filepath.Join(root, "shim")
	run(javac, "--release", "8", "-d", api, "apexload/host/SlotLoader.java")
	service := write("src/shared/Service.java", `package shared; public interface Service {String value();}`)
	common := write("src/shared/Common.java", `package shared; public class Common {public static int count;}`)
	sealed := write("src/sealed/One.java", `package sealed; public class One {}`)
	bad := write("src/shared/NeedsPrivate.java", `package shared; public class NeedsPrivate {public static Object value() throws Exception {return Class.forName("privatecode.Provider");}}`)
	run(javac, "--release", "8", "-d", shared, service, common, sealed, bad)
	provider := write("src/privatecode/Provider.java", `package privatecode; public class Provider implements shared.Service {public String value(){return "private";}}`)
	privateState := write("src/privatecode/State.java", `package privatecode; public class State {public static int count;}`)
	run(javac, "--release", "8", "-cp", shared, "-d", private, provider, privateState)
	second := write("src/sealed/Two.java", `package sealed; public class Two {}`)
	run(javac, "--release", "8", "-d", extra, second)
	lr := write("src/lrapi/lr.java", `package lrapi; public class lr {}`)
	run(javac, "--release", "8", "-d", shim, lr)
	write("private/META-INF/services/shared.Service", "privatecode.Provider\n")
	write("private/duplicate.txt", "private")
	write("shared/duplicate.txt", "shared")
	jar := func(dir, name, manifest string) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, e := os.Create(path)
		if e != nil {
			t.Fatal(e)
		}
		z := zip.NewWriter(file)
		if manifest != "" {
			w, e := z.Create("META-INF/MANIFEST.MF")
			if e != nil {
				t.Fatal(e)
			}
			_, e = w.Write([]byte(manifest))
			if e != nil {
				t.Fatal(e)
			}
		}
		e = filepath.WalkDir(dir, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(dir, path)
			if e != nil {
				return e
			}
			w, e := z.Create(filepath.ToSlash(rel))
			if e != nil {
				return e
			}
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			_, e = w.Write(data)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		if e = z.Close(); e != nil {
			t.Fatal(e)
		}
		if e = file.Close(); e != nil {
			t.Fatal(e)
		}
		return path
	}
	sharedJar := jar(shared, "shared.jar", "Manifest-Version: 1.0\r\nSealed: true\r\n\r\n")
	privateJar := jar(private, "private.jar", "")
	extraJar := jar(extra, "extra.jar", "")
	shimJar := jar(shim, "shim.jar", "")
	probe := write("LoaderCheck.java", sharedLoaderProbe)
	run(javac, "--release", "8", "-cp", api, "-d", api, probe)
	run(java, "-cp", api, "LoaderCheck", sharedJar, privateJar, extraJar, shimJar)
}

const sharedLoaderProbe = `
import java.net.*;import java.util.*;import apexload.host.SlotLoader;
public class LoaderCheck {
 static URL url(String p)throws Exception{return new java.io.File(p).toURI().toURL();}
 public static void main(String[] args)throws Exception {
  URL sharedURL=url(args[0]),privateURL=url(args[1]),extraURL=url(args[2]),shimURL=url(args[3]);
  try(URLClassLoader shared=new URLClassLoader(new URL[]{sharedURL,extraURL,shimURL},LoaderCheck.class.getClassLoader());
      SlotLoader a=new SlotLoader(shimURL,new URL[]{privateURL},shared);
      SlotLoader b=new SlotLoader(shimURL,new URL[]{privateURL},shared)){
   Class<?> common=a.loadClass("shared.Common");
   if(common!=b.loadClass("shared.Common") || common.getClassLoader()!=shared)throw new AssertionError("shared identity");
   if(a.loadClass("privatecode.State")==b.loadClass("privatecode.State"))throw new AssertionError("private static leakage");
   if(a.loadClass("lrapi.lr")==b.loadClass("lrapi.lr"))throw new AssertionError("shim leakage");
   if(!a.getResource("duplicate.txt").toString().contains("private.jar"))throw new AssertionError("resource precedence");
   List<URL> resources=Collections.list(a.getResources("duplicate.txt"));
   if(resources.size()!=2 || !resources.get(0).toString().contains("private.jar") || !resources.get(1).toString().contains("shared.jar"))throw new AssertionError(resources);
   Class<?> service=shared.loadClass("shared.Service");
   ClassLoader saved=Thread.currentThread().getContextClassLoader();
   try {
    Thread.currentThread().setContextClassLoader(a);
    Object provider=ServiceLoader.load(service).iterator().next();
    if(provider.getClass().getClassLoader()!=a)throw new AssertionError("TCCL service discovery");
    Thread.currentThread().setContextClassLoader(b);
    if(ServiceLoader.load(service).iterator().next().getClass()==provider.getClass())throw new AssertionError("provider shared across slots");
   } finally {Thread.currentThread().setContextClassLoader(saved);}
   try{shared.loadClass("shared.NeedsPrivate").getMethod("value").invoke(null);throw new AssertionError("shared dependency resolved private class");}
   catch(java.lang.reflect.InvocationTargetException expected){if(!(expected.getCause() instanceof ClassNotFoundException))throw expected;}
   shared.loadClass("sealed.One");
   try{shared.loadClass("sealed.Two");throw new AssertionError("sealing silently bypassed");}catch(SecurityException expected){}
  }
  // Missing private shim must never fall through to the shared loader's copy.
  try(URLClassLoader shared=new URLClassLoader(new URL[]{shimURL},LoaderCheck.class.getClassLoader());
      SlotLoader slot=new SlotLoader(privateURL,new URL[]{privateURL},shared)){
   try{slot.loadClass("lrapi.lr");throw new AssertionError("shared lr accepted");}catch(ClassNotFoundException expected){}
  }
 }
}`
