package apexload.host;

import java.io.IOException;
import java.net.URL;
import java.net.URLClassLoader;
import java.util.Collections;
import java.util.Enumeration;
import java.util.LinkedHashSet;

/** Private slot loader. The shim URL must identify an approved shim-only JAR;
 * the parent contains only approved shared dependencies and host bridge types.
 * Artifact qualification and collision checks precede construction. */
public final class SlotLoader extends URLClassLoader {
    static { registerAsParallelCapable(); }

    public SlotLoader(URL shim, URL[] script, ClassLoader sharedParent) {
        super(entries(shim, script), sharedParent);
    }

    private static URL[] entries(URL shim, URL[] script) {
        if (shim == null || script == null || script.length == 0)
            throw new IllegalArgumentException("shim and script classpath required");
        URL[] result = new URL[script.length + 1];
        result[0] = shim;
        System.arraycopy(script, 0, result, 1, script.length);
        return result;
    }

    @Override protected Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
        synchronized (getClassLoadingLock(name)) {
            Class<?> found = findLoadedClass(name);
            if (found == null) {
                if (name.startsWith("java.") || name.startsWith("javax.") || name.startsWith("jdk.")
                        || name.startsWith("sun.") || name.startsWith("apexload.")) {
                    found = getParent().loadClass(name);
                } else {
                    try { found = findClass(name); }
                    catch (ClassNotFoundException missing) {
                        // Never fall through to a system/shared copy of lr.
                        if (name.startsWith("lrapi.")) throw missing;
                        found = getParent().loadClass(name);
                    }
                }
            }
            if (resolve) resolveClass(found);
            return found;
        }
    }

    @Override public URL getResource(String name) {
        if (name.startsWith("apexload/")) return getParent().getResource(name);
        URL own = findResource(name);
        if (own != null || name.startsWith("lrapi/")) return own;
        return getParent().getResource(name);
    }

    @Override public Enumeration<URL> getResources(String name) throws IOException {
        if (name.startsWith("apexload/")) return getParent().getResources(name);
        LinkedHashSet<URL> result = new LinkedHashSet<URL>();
        result.addAll(Collections.list(findResources(name)));
        if (!name.startsWith("lrapi/")) result.addAll(Collections.list(getParent().getResources(name)));
        return Collections.enumeration(result);
    }
}
