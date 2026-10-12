import java.io.BufferedReader;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.InputStreamReader;
import java.io.IOException;
import java.io.OutputStream;
import java.io.PrintStream;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.lang.reflect.Modifier;
import java.net.URL;
import java.net.URLClassLoader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.nio.file.StandardOpenOption;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

import lrapi.lr;

public final class Harness {
    private static final String SESSION_PREFIX = "APEXLOAD_JAVA_VUSER_V2 ";
    private static final String IPC_CONTRACT = "apexload.java_vuser.ipc.v2";
    private static final String EVENT_CONTRACT = "apexload.java_vuser.lrapi_events.v2";
    private static final int MAX_DIAGNOSTIC_BYTES = 128 * 1024;
    private static final long MAX_DIAGNOSTIC_FILE_BYTES = 256L * 1024L;
    private static final int MAX_ERROR_BYTES = 64 * 1024;
    private static final String TRUNCATION_MARKER = "...truncated";
    private static final long EXIT_HALT_GRACE_MS = 2000L;

    private Harness() {
    }

    public static void main(String[] args) throws Exception {
        if (hasFlag(args, "--health")) {
            System.out.println(SESSION_PREFIX
                    + "{\"contract\":\"" + IPC_CONTRACT
                    + "\",\"kind\":\"capabilities\",\"event_contract\":\"" + EVENT_CONTRACT
                    + "\",\"features\":[\"java_vuser_phase_lifecycle_v1\",\"java_vuser_persistent_lifecycle_v1\",\"java_vuser_session_frame_token_v1\",\"java_vuser_validation_only_v1\",\"java_vuser_dedicated_classloader_v1\"" + (nativeServicesAvailable() ? ",\"native_data_local_v1\"" : "") + "]}");
            return;
        }
        BufferedReader reader = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8));
        if (hasFlag(args, "--session")) {
            runSession(args, reader);
            return;
        }
        runOneShot(Command.from(args, readCommandLine(reader)));
    }

    private static void runOneShot(Command command) throws Exception {
        validateContracts(command);
        if (!command.sourceArchiveRef.isEmpty()) {
            System.setProperty("apexload.java_vuser.source_archive_ref", command.sourceArchiveRef);
        }
        long start = System.currentTimeMillis();
        if (command.validationOnly) {
            try {
                validateActions(command);
                System.out.println(summaryJson(command, true, "", System.currentTimeMillis() - start));
            } catch (Throwable ex) {
                System.out.println(summaryJson(command, false, boundedError(unwrap(ex).toString()), System.currentTimeMillis() - start));
                exitProcess(1);
            }
            return;
        }
        lr.__apexload_begin_invocation(command.requestId, command.lrParameters);
        try {
            try (ActionsHandle actions = instantiateActions(command)) {
                invokeLifecycleWithContext(actions, command.method, false);
            }
            lr.__apexload_end_invocation();
            long duration = System.currentTimeMillis() - start;
            System.out.println(summaryJson(command, true, "", duration));
        } catch (Throwable ex) {
            lr.__apexload_end_invocation();
            System.out.println(summaryJson(command, false, boundedError(unwrap(ex).toString()), System.currentTimeMillis() - start));
            exitProcess(1);
        }
    }

    // Session mode owns exactly one Actions instance for one engine VUser.
    // Protocol stdout is never exposed to user code; user stdout/stderr is
    // captured into bounded result fields and mirrored to a bounded diagnostic
    // side channel so crashes and timeouts still leave useful evidence.
    private static void runSession(String[] args, BufferedReader reader) throws Exception {
        PrintStream protocolOut = System.out;
        PrintStream processStderr = System.err;
        DiagnosticBuffer userStdout = new DiagnosticBuffer(MAX_DIAGNOSTIC_BYTES);
        DiagnosticBuffer userStderr = new DiagnosticBuffer(MAX_DIAGNOSTIC_BYTES);
        System.setOut(new PrintStream(userStdout, true, StandardCharsets.UTF_8.name()));
        System.setErr(new PrintStream(userStderr, true, StandardCharsets.UTF_8.name()));

        // Opt-in pod preparation: the saved reader remains the control input.
        // No target class is loaded or initialized before the first IPC command.
        String readyNonce = Command.argValue(args, "--host-ready-nonce");
        if (!readyNonce.isEmpty()) {
            if (!readyNonce.matches("[a-f0-9]{64}")) {
                throw new IllegalArgumentException("Invalid host readiness nonce");
            }
            System.setIn(new ByteArrayInputStream(new byte[0]));
            try {Class.forName("apexload.bridge.NativeSession",false,Harness.class.getClassLoader());protocolOut.println("APEXLOAD_JAVA_HOST_NATIVE_V1 "+readyNonce);}catch(ClassNotFoundException unavailable){}
            protocolOut.println("APEXLOAD_JAVA_HOST_READY_V1 " + readyNonce);
            protocolOut.flush();
        }

        ActionsHandle actions = null;
        byte[] sessionSecret = null;
        String sessionClass = "";
        String sessionConfiguration = "";
        boolean fatalSession = false;
        try {
            String line;
            while (!fatalSession && (line = reader.readLine()) != null) {
                if (line.trim().isEmpty()) {
                    continue;
                }
                userStdout.reset();
                userStderr.reset();
                Command command = Command.fromSession(args, line);
                long start = System.currentTimeMillis();
                boolean success = false;
                String error = "";
                String frameToken = "";
                boolean invocationStarted = false;
                boolean nativeStarted = false;
                try {
                    if (!IPC_CONTRACT.equals(command.protocolVersion)) {
                        fatalSession = true;
                        throw new IllegalStateException("unsupported persistent IPC contract");
                    }
                    if (!EVENT_CONTRACT.equals(command.eventContract)) {
                        fatalSession = true;
                        throw new IllegalStateException("unsupported LoadRunner event contract");
                    }
                    if (command.requestId.isEmpty()) {
                        fatalSession = true;
                        throw new IllegalStateException("persistent request_id is required");
                    }
                    if (sessionSecret == null) {
                        if (!validSessionSecret(command.sessionSecret)) {
                            fatalSession = true;
                            throw new IllegalStateException("persistent session_secret is invalid");
                        }
                        sessionSecret = decodeHex(command.sessionSecret);
                        command.sessionSecret = "";
                    } else if (!command.sessionSecret.isEmpty()) {
                        fatalSession = true;
                        throw new IllegalStateException("persistent session_secret may only be bootstrapped once");
                    }
                    frameToken = sessionFrameToken(sessionSecret, command.requestId);
                    if (command.nativeServices && (!nativeServicesAvailable() || !isLifecycleMethod(command.method))) {
                        fatalSession = true;
                        throw new IllegalStateException("native data lifecycle unavailable");
                    }
                    String configuration = command.sessionConfiguration();
                    if (!command.sourceArchiveRef.isEmpty()) {
                        System.setProperty("apexload.java_vuser.source_archive_ref", command.sourceArchiveRef);
                    }
                    lr.__apexload_begin_invocation(command.requestId, command.lrParameters);
                    invocationStarted = true;
                    if (actions == null) {
                        actions = instantiateActions(command);
                        sessionClass = command.className;
                        sessionConfiguration = configuration;
                    } else if (!sessionClass.equals(command.className) || !sessionConfiguration.equals(configuration)) {
                        fatalSession = true;
                        throw new IllegalStateException("persistent session configuration changed");
                    }
                    if (command.nativeServices) {
                        Class.forName("apexload.bridge.NativeSession").getMethod("begin", String.class, byte[].class, BufferedReader.class, PrintStream.class)
                            .invoke(null, command.requestId, sessionSecret, reader, protocolOut);
                        nativeStarted = true;
                    }
                    invokeLifecycleWithContext(
                            actions,
                            command.method,
                            !"full_lifecycle".equalsIgnoreCase(command.method));
                    success = true;
                } catch (Throwable ex) {
                    // Construction/configuration failures leave no trustworthy
                    // Actions state and permanently poison this JVM. Ordinary
                    // lifecycle failures are reported but retain the instance,
                    // matching LoadRunner's per-VUser lifecycle.
                    if (actions == null) {
                        fatalSession = true;
                    }
                    error = boundedError(unwrap(ex).toString());
                } finally {
                    if (nativeStarted) {
                        try { Class.forName("apexload.bridge.NativeSession").getMethod("end").invoke(null); }
                        catch (Throwable nativeEndError) { fatalSession = true; success = false; }
                    }
                    if (invocationStarted) {
                        try {
                            lr.__apexload_end_invocation();
                        } catch (Throwable endError) {
                            success = false;
                            if (error.isEmpty()) {
                                error = boundedError(unwrap(endError).toString());
                            }
                        }
                    }
                }
                String envelope = "{\"contract\":\"" + IPC_CONTRACT
                        + "\",\"kind\":\"result\",\"request_id\":\"" + escape(command.requestId)
                        + "\",\"frame_token\":\"" + escape(frameToken)
                        + "\",\"success\":" + success
                        + ",\"fatal\":" + fatalSession
                        + ",\"error\":\"" + escape(error)
                        + "\",\"class_name\":\"" + escape(command.className)
                        + "\",\"method\":\"" + escape(command.method)
                        + "\",\"duration_ms\":" + (System.currentTimeMillis() - start)
                        + ",\"stdout\":\"" + escape(userStdout.text())
                        + "\",\"stderr\":\"" + escape(userStderr.text())
                        + "\",\"stdout_truncated\":" + userStdout.truncated()
                        + ",\"stderr_truncated\":" + userStderr.truncated()
                        + ",\"event_fallback_used\":" + lr.__apexload_event_fallback_used()
                        + ",\"events_truncated\":" + (lr.__apexload_event_capture_truncated()
                            || (lr.__apexload_event_fallback_used() && userStderr.truncated()))
                        + "}";
                protocolOut.println(SESSION_PREFIX + envelope);
                protocolOut.flush();
            }
        } finally {
            System.setOut(protocolOut);
            System.setErr(processStderr);
            if (actions != null) {
                try {
                    actions.close();
                } catch (IOException closeError) {
                    processStderr.println("java_vuser Actions classloader close failed");
                }
            }
            if (sessionSecret != null) {
                Arrays.fill(sessionSecret, (byte) 0);
            }
        }
        // User libraries commonly retain non-daemon pools. Explicit process
        // termination after EOF prevents a clean VUser teardown from waiting
        // for unrelated user threads until the engine deadline.
        exitProcess(fatalSession ? 1 : 0);
    }

    private static void exitProcess(final int status) {
        // System.exit waits indefinitely for hostile or buggy shutdown hooks.
        // Keep their normal grace period, then force the per-VUser JVM down so
        // teardown always releases the generator capacity slot.
        Thread watchdog = new Thread(() -> {
            try {
                Thread.sleep(EXIT_HALT_GRACE_MS);
            } catch (InterruptedException ignored) {
                Thread.currentThread().interrupt();
            }
            Runtime.getRuntime().halt(status);
        }, "apexload-java-vuser-halt-watchdog");
        watchdog.setDaemon(true);
        watchdog.start();
        System.exit(status);
        Runtime.getRuntime().halt(status);
    }

    private static void validateContracts(Command command) {
        if (!command.protocolVersion.isEmpty() && !IPC_CONTRACT.equals(command.protocolVersion)) {
            throw new IllegalStateException("unsupported IPC contract");
        }
        if (!command.eventContract.isEmpty() && !EVENT_CONTRACT.equals(command.eventContract)) {
            throw new IllegalStateException("unsupported LoadRunner event contract");
        }
    }

    private static void invokeLifecycle(Object actions, String method, boolean allowControlledContinuation) throws Exception {
        RunResult runResult = new RunResult();
        if ("full_lifecycle".equalsIgnoreCase(method)) {
            runResult.optional(invokeFirstPresent(actions, false, "init", "vuser_init", "initialize"));
            runResult.required(invokeFirstPresent(actions, false, "action", "Action", "run"), "action/Action/run");
            runResult.optional(invokeFirstPresent(actions, false, "end", "vuser_end", "teardown"));
        } else if (isLifecycleMethod(method)) {
            runResult.required(invokeIfPresent(actions, method, allowControlledContinuation), method);
        } else {
            throw new IllegalStateException("unsupported lifecycle method: " + method);
        }
    }

    private static void invokeLifecycleWithContext(
            ActionsHandle actions,
            String method,
            boolean allowControlledContinuation) throws Exception {
        Thread thread = Thread.currentThread();
        ClassLoader previous = thread.getContextClassLoader();
        thread.setContextClassLoader(actions.loader.loader);
        try {
            invokeLifecycle(actions.actions, method, allowControlledContinuation);
        } finally {
            thread.setContextClassLoader(previous);
        }
    }

    private static boolean nativeServicesAvailable() {
        try { Class.forName("apexload.bridge.NativeSession", false, Harness.class.getClassLoader()); return true; }
        catch (ClassNotFoundException absent) { return false; }
    }

    private static boolean isLifecycleMethod(String method) {
        return "init".equals(method) || "vuser_init".equals(method) || "initialize".equals(method)
                || "action".equals(method) || "Action".equals(method) || "run".equals(method)
                || "end".equals(method) || "vuser_end".equals(method) || "teardown".equals(method);
    }

    private static InvocationResult invokeFirstPresent(
            Object target,
            boolean allowControlledContinuation,
            String... names) throws Exception {
        Method method = findFirstValidLifecycleMethod(target.getClass(), false, names);
        if (method == null) {
            return InvocationResult.missing(String.join("/", names));
        }
        return invokeLifecycleMethod(target, method, allowControlledContinuation);
    }

    private static Throwable unwrap(Throwable ex) {
        if (ex instanceof InvocationTargetException && ((InvocationTargetException) ex).getCause() != null) {
            return ((InvocationTargetException) ex).getCause();
        }
        return ex;
    }

    private static String summaryJson(Command command, boolean success, String error, long duration) {
        return "{\"contract\":\"" + IPC_CONTRACT
                + "\",\"kind\":\"result\""
                + ",\"success\":" + success
                + ",\"error\":\"" + escape(error)
                + "\",\"request_id\":\"" + escape(command.requestId)
                + "\",\"class_name\":\"" + escape(command.className)
                + "\",\"method\":\"" + escape(command.method)
                + "\",\"duration_ms\":" + duration
                + ",\"event_fallback_used\":" + lr.__apexload_event_fallback_used()
                + ",\"events_truncated\":" + lr.__apexload_event_capture_truncated()
                + "}";
    }

    private static InvocationResult invokeIfPresent(
            Object target,
            String name,
            boolean allowControlledContinuation) throws Exception {
        Method method = findFirstValidLifecycleMethod(target.getClass(), false, name);
        if (method == null) {
            return InvocationResult.missing(name);
        }
        return invokeLifecycleMethod(target, method, allowControlledContinuation);
    }

    private static InvocationResult invokeLifecycleMethod(
            Object target,
            Method method,
            boolean allowControlledContinuation) throws Exception {
        String name = method.getName();
        Object returned;
        try {
            returned = method.invoke(target);
        } catch (InvocationTargetException ex) {
            Throwable cause = ex.getCause();
            if (cause instanceof lr.ExitSignal) {
                lr.ExitSignal signal = (lr.ExitSignal) cause;
                int option = signal.continuationOption();
                if (allowControlledContinuation
                        && (option == lr.EXIT_ACTION_AND_CONTINUE
                            || option == lr.EXIT_ITERATION_AND_CONTINUE)) {
                    lr.__apexload_handle_exit(option, signal.exitStatus());
                    // This is a controlled Java-flow unwind. The requested exit
                    // status remains in the runtime/transaction_ignored events,
                    // while the aggregate call succeeds so the engine does not
                    // mistake "continue" for a fatal VUser error.
                    return InvocationResult.exited(name, lr.PASS, option);
                }
                // VUser-wide continuation modes require an explicit engine flow
                // contract and are deliberately not claimed as executable.
                throw ex;
            }
            throw ex;
        }
        int status = lr.PASS;
        if (returned instanceof Number) {
            status = ((Number) returned).intValue();
        }
        return InvocationResult.invoked(name, status);
    }

    private static ActionsHandle instantiateActions(Command command) throws Exception {
        UserClassLoaderHandle loader = UserClassLoaderHandle.open(command);
        try {
            if (command.startupTimeoutMs <= 0) {
                return new ActionsHandle(instantiateWithContext(command.className, loader.loader, command.dedicatedClassLoader), loader);
            }
            ExecutorService executor = Executors.newSingleThreadExecutor();
            Future<Object> future = executor.submit(
                    () -> instantiateWithContext(command.className, loader.loader, command.dedicatedClassLoader));
            try {
                return new ActionsHandle(future.get(command.startupTimeoutMs, TimeUnit.MILLISECONDS), loader);
            } catch (TimeoutException ex) {
                future.cancel(true);
                throw new StartupTimeoutException("startup timeout after " + command.startupTimeoutMs + "ms while loading " + command.className);
            } catch (ExecutionException ex) {
                Throwable cause = ex.getCause();
                if (cause instanceof Exception) {
                    throw (Exception) cause;
                }
                if (cause instanceof Error) {
                    throw (Error) cause;
                }
                throw new RuntimeException(cause);
            } finally {
                executor.shutdownNow();
            }
        } catch (Exception | Error ex) {
            loader.close();
            throw ex;
        }
    }

    private static Object instantiateWithContext(String className, ClassLoader loader, boolean dedicated) throws Exception {
        Thread thread = Thread.currentThread();
        ClassLoader previous = thread.getContextClassLoader();
        thread.setContextClassLoader(loader);
        try {
            Class<?> actions = Class.forName(className, true, loader);
            requireDedicatedLoader(actions, loader, dedicated);
            return actions.getDeclaredConstructor().newInstance();
        } finally {
            thread.setContextClassLoader(previous);
        }
    }

    private static void validateActions(Command command) throws Exception {
        try (UserClassLoaderHandle loader = UserClassLoaderHandle.open(command)) {
            Thread thread = Thread.currentThread();
            ClassLoader previous = thread.getContextClassLoader();
            thread.setContextClassLoader(loader.loader);
            try {
                Class<?> actions = Class.forName(command.className, false, loader.loader);
                requireDedicatedLoader(actions, loader.loader, command.dedicatedClassLoader);
                validateActionsType(actions);
                if ("full_lifecycle".equalsIgnoreCase(command.method)) {
                    validateFirstLifecycleMethod(actions, false, "init", "vuser_init", "initialize");
                    validateFirstLifecycleMethod(actions, true, "action", "Action", "run");
                    validateFirstLifecycleMethod(actions, false, "end", "vuser_end", "teardown");
                } else if (isLifecycleMethod(command.method)) {
                    findFirstValidLifecycleMethod(actions, true, command.method);
                } else {
                    throw new IllegalStateException("unsupported lifecycle method: " + command.method);
                }
            } finally {
                thread.setContextClassLoader(previous);
            }
        }
    }

    private static void validateActionsType(Class<?> actions) throws NoSuchMethodException {
        int modifiers = actions.getModifiers();
        if (!Modifier.isPublic(modifiers) || Modifier.isAbstract(modifiers) || actions.isInterface()) {
            throw new IllegalStateException(
                    "Actions target must be a public, concrete class: " + actions.getName());
        }
        // Validate public constructor access without initializing or creating
        // an Actions instance during launch preflight.
        actions.getConstructor();
    }

    private static void requireDedicatedLoader(Class<?> actions, ClassLoader expected, boolean dedicated) {
        if (dedicated && actions.getClassLoader() != expected) {
            throw new IllegalStateException("dedicated class loader could not isolate the target class");
        }
    }

    private static void validateFirstLifecycleMethod(
            Class<?> actions,
            boolean required,
            String... names) throws NoSuchMethodException {
        findFirstValidLifecycleMethod(actions, required, names);
    }

    private static Method findFirstValidLifecycleMethod(
            Class<?> actions,
            boolean required,
            String... names) throws NoSuchMethodException {
        IllegalStateException firstInvalid = null;
        for (String name : names) {
            for (Method method : actions.getDeclaredMethods()) {
                if (!name.equals(method.getName()) || method.isSynthetic() || method.isBridge()) {
                    continue;
                }
                try {
                    validateLifecycleMethod(method);
                    return method;
                } catch (IllegalStateException invalid) {
                    if (firstInvalid == null) {
                        firstInvalid = invalid;
                    }
                }
            }
        }
        if (firstInvalid != null) {
            throw firstInvalid;
        }
        if (required) {
            throw new NoSuchMethodException(
                    "required zero-argument lifecycle method was not found: "
                            + String.join("/", names));
        }
        return null;
    }

    private static void validateLifecycleMethod(Method method) {
        int modifiers = method.getModifiers();
        if (!Modifier.isPublic(modifiers) || Modifier.isStatic(modifiers)
                || method.getParameterCount() != 0 || method.getReturnType() != Integer.TYPE) {
            throw new IllegalStateException(
                    "lifecycle method " + method.getName()
                            + " must be a public non-static zero-argument method returning int");
        }
    }

    private static final class ActionsHandle implements AutoCloseable {
        final Object actions;
        final UserClassLoaderHandle loader;

        ActionsHandle(Object actions, UserClassLoaderHandle loader) {
            this.actions = actions;
            this.loader = loader;
        }

        @Override
        public void close() throws IOException {
            loader.close();
        }
    }

    private static final class UserClassLoaderHandle implements AutoCloseable {
        final ClassLoader loader;
        final URLClassLoader closeable;

        private UserClassLoaderHandle(ClassLoader loader, URLClassLoader closeable) {
            this.loader = loader;
            this.closeable = closeable;
        }

        static UserClassLoaderHandle open(Command command) throws Exception {
            ClassLoader parent = Harness.class.getClassLoader();
            if (!command.dedicatedClassLoader) {
                return new UserClassLoaderHandle(parent, null);
            }
            if (command.userClasspath.isEmpty()) {
                throw new IllegalStateException("dedicated class loader requires a non-empty user_classpath");
            }
            URL[] urls = new URL[command.userClasspath.size()];
            for (int index = 0; index < command.userClasspath.size(); index++) {
                urls[index] = new File(command.userClasspath.get(index)).toURI().toURL();
            }
            URLClassLoader loader = new URLClassLoader(urls, parent);
            return new UserClassLoaderHandle(loader, loader);
        }

        @Override
        public void close() throws IOException {
            if (closeable != null) {
                closeable.close();
            }
        }
    }

    // Keep the one-shot result envelope bounded even when user Actions code
    // produces an unusually large exception message.
    private static String boundedError(String value) {
        if (value == null) {
            return "";
        }
        byte[] bytes = value.getBytes(StandardCharsets.UTF_8);
        if (bytes.length <= MAX_ERROR_BYTES) {
            return value;
        }
        return new String(bytes, 0, MAX_ERROR_BYTES, StandardCharsets.UTF_8) + TRUNCATION_MARKER;
    }

    private static final class StartupTimeoutException extends IllegalStateException {
        StartupTimeoutException(String message) {
            super(message);
        }
    }

    private static final class RunResult {
        void optional(InvocationResult result) {
            if (result.invoked) {
                requirePass(result);
            }
        }

        void required(InvocationResult result, String name) {
            if (!result.invoked) {
                throw new IllegalStateException("required zero-argument lifecycle method was not found: " + name);
            }
            requirePass(result);
        }

        private void requirePass(InvocationResult result) {
            if (result.status != lr.PASS) {
                throw new IllegalStateException("lifecycle method " + result.name + " returned non-PASS status " + result.status);
            }
        }
    }

    private static final class InvocationResult {
        final String name;
        final boolean invoked;
        final int status;
        final int continuationOption;

        private InvocationResult(String name, boolean invoked, int status, int continuationOption) {
            this.name = name;
            this.invoked = invoked;
            this.status = status;
            this.continuationOption = continuationOption;
        }

        static InvocationResult missing(String name) {
            return new InvocationResult(name, false, lr.PASS, -1);
        }

        static InvocationResult invoked(String name, int status) {
            return new InvocationResult(name, true, status, -1);
        }

        static InvocationResult exited(String name, int status, int continuationOption) {
            return new InvocationResult(name, true, status, continuationOption);
        }
    }

    private static String readCommandLine(BufferedReader reader) throws Exception {
        String line = reader.readLine();
        return line == null ? "" : line;
    }

    private static boolean validSessionSecret(String value) {
        if (value == null || value.length() != 64) {
            return false;
        }
        for (int index = 0; index < value.length(); index++) {
            char ch = value.charAt(index);
            if (!((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f'))) {
                return false;
            }
        }
        return true;
    }

    private static byte[] decodeHex(String value) {
        byte[] decoded = new byte[value.length() / 2];
        for (int index = 0; index < decoded.length; index++) {
            int high = Character.digit(value.charAt(index * 2), 16);
            int low = Character.digit(value.charAt(index * 2 + 1), 16);
            decoded[index] = (byte) ((high << 4) | low);
        }
        return decoded;
    }

    private static String sessionFrameToken(byte[] secret, String requestId) throws Exception {
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(secret, "HmacSHA256"));
        mac.update(IPC_CONTRACT.getBytes(StandardCharsets.UTF_8));
        mac.update((byte) 0);
        byte[] digest = mac.doFinal(requestId.getBytes(StandardCharsets.UTF_8));
        StringBuilder encoded = new StringBuilder(digest.length * 2);
        for (byte value : digest) {
            encoded.append(Character.forDigit((value >>> 4) & 0xf, 16));
            encoded.append(Character.forDigit(value & 0xf, 16));
        }
        Arrays.fill(digest, (byte) 0);
        return encoded.toString();
    }

    private static boolean hasFlag(String[] args, String key) {
        for (int index = 0; index < args.length; index++) {
            String arg = args[index];
            if (takesValue(arg)) {
                index++;
                continue;
            }
            if (key.equals(arg)) {
                return true;
            }
        }
        return false;
    }

    private static boolean takesValue(String arg) {
        return "--class".equals(arg) || "--method".equals(arg)
                || "--host-ready-nonce".equals(arg) || "--source-archive-ref".equals(arg) || "--startup-timeout-ms".equals(arg);
    }

    private static String escape(String value) {
        if (value == null) {
            return "";
        }
        StringBuilder out = new StringBuilder(value.length() + 8);
        for (int i = 0; i < value.length(); i++) {
            char ch = value.charAt(i);
            switch (ch) {
                case '\\': out.append("\\\\"); break;
                case '"': out.append("\\\""); break;
                case '\n': out.append("\\n"); break;
                case '\r': out.append("\\r"); break;
                case '\t': out.append("\\t"); break;
                case '\b': out.append("\\b"); break;
                case '\f': out.append("\\f"); break;
                default:
                    if (ch < 0x20) {
                        out.append(String.format("\\u%04x", (int) ch));
                    } else {
                        out.append(ch);
                    }
            }
        }
        return out.toString();
    }

    private static final class Command {
        String className = "Actions";
        String method = "full_lifecycle";
        String sourceArchiveRef = "";
        String requestId = "";
        String sessionSecret = "";
        String protocolVersion = "";
        String eventContract = "";
        int startupTimeoutMs = 0;
        boolean dedicatedClassLoader = false;
        boolean validationOnly = false;
        boolean nativeServices = false;
        List<String> userClasspath = new ArrayList<>();
        Map<String, String> lrParameters = new LinkedHashMap<>();

        static Command from(String[] args, String jsonLine) {
            return parse(args, jsonLine, false);
        }

        static Command fromSession(String[] args, String jsonLine) {
            return parse(args, jsonLine, true);
        }

        private static Command parse(String[] args, String jsonLine, boolean session) {
            Command command = new Command();
            if (session) {
                command.className = firstNonEmpty(jsonString(jsonLine, "class_name"), argValue(args, "--class"), command.className);
                command.method = firstNonEmpty(jsonString(jsonLine, "method"), argValue(args, "--method"), command.method);
                command.sourceArchiveRef = firstNonEmpty(jsonString(jsonLine, "source_archive_ref"), argValue(args, "--source-archive-ref"));
                command.startupTimeoutMs = parsePositiveInt(firstNonEmpty(jsonNumber(jsonLine, "startup_timeout_ms"), argValue(args, "--startup-timeout-ms")));
            } else {
                command.className = firstNonEmpty(argValue(args, "--class"), jsonString(jsonLine, "class_name"), command.className);
                command.method = firstNonEmpty(argValue(args, "--method"), jsonString(jsonLine, "method"), command.method);
                command.sourceArchiveRef = firstNonEmpty(argValue(args, "--source-archive-ref"), jsonString(jsonLine, "source_archive_ref"));
                command.startupTimeoutMs = parsePositiveInt(firstNonEmpty(argValue(args, "--startup-timeout-ms"), jsonNumber(jsonLine, "startup_timeout_ms")));
            }
            command.requestId = jsonString(jsonLine, "request_id");
            command.sessionSecret = jsonString(jsonLine, "session_secret");
            command.protocolVersion = jsonString(jsonLine, "protocol_version");
            command.eventContract = jsonString(jsonLine, "event_contract");
            command.dedicatedClassLoader = jsonBoolean(jsonLine, "dedicated_class_loader");
            command.nativeServices = jsonBoolean(jsonLine, "native_services");
            command.validationOnly = hasFlag(args, "--validate") || jsonBoolean(jsonLine, "validation_only");
            command.userClasspath.addAll(jsonStringArray(jsonLine, "user_classpath"));
            String paramsObject = jsonObject(jsonLine, "params");
            command.lrParameters.putAll(jsonStringMap(jsonObject(paramsObject, "parameters")));
            command.lrParameters.putAll(jsonStringMap(jsonObject(paramsObject, "lr_parameters")));
            return command;
        }

        String sessionConfiguration() {
            return className + "\u0000"
                    + sourceArchiveRef + "\u0000"
                    + dedicatedClassLoader + "\u0000"
                    + nativeServices + "\u0000"
                    + String.join("\u0000", userClasspath);
        }

        private static String argValue(String[] args, String key) {
            for (int i = 0; i + 1 < args.length; i++) {
                if (key.equals(args[i])) {
                    return args[i + 1];
                }
            }
            return "";
        }

        // Finds an exact top-level field without being confused by a same-named
        // field nested inside user-controlled params. The harness intentionally
        // avoids a third-party JSON dependency, so all runtime-steering fields
        // must use this structural scan instead of a raw substring search.
        private static int topLevelValueStart(String json, String key) {
            if (json == null || json.isEmpty()) {
                return -1;
            }
            int index = nextNonWhitespace(json, 0);
            if (index < 0 || json.charAt(index) != '{') {
                return -1;
            }
            index++;
            while (index < json.length()) {
                index = skipWhitespaceAndCommas(json, index);
                if (index < 0 || index >= json.length() || json.charAt(index) == '}') {
                    return -1;
                }
                ParsedString field = parseJsonString(json, index);
                if (field == null) {
                    return -1;
                }
                index = skipWhitespace(json, field.next);
                if (index >= json.length() || json.charAt(index) != ':') {
                    return -1;
                }
                int valueStart = nextNonWhitespace(json, index + 1);
                if (valueStart < 0) {
                    return -1;
                }
                if (key.equals(field.value)) {
                    return valueStart;
                }
                char valueType = json.charAt(valueStart);
                if (valueType == '"') {
                    ParsedString value = parseJsonString(json, valueStart);
                    if (value == null) {
                        return -1;
                    }
                    index = value.next;
                } else if (valueType == '{' || valueType == '[') {
                    char close = valueType == '{' ? '}' : ']';
                    int end = scanCompositeEnd(json, valueStart, valueType, close);
                    if (end < 0) {
                        return -1;
                    }
                    index = end + 1;
                } else {
                    index = valueStart;
                    while (index < json.length() && json.charAt(index) != ',' && json.charAt(index) != '}') {
                        index++;
                    }
                }
            }
            return -1;
        }

        private static String jsonString(String json, String key) {
            int start = topLevelValueStart(json, key);
            if (start < 0 || json.charAt(start) != '"') {
                return "";
            }
            ParsedString parsed = parseJsonString(json, start);
            return parsed == null ? "" : parsed.value;
        }

        private static String jsonNumber(String json, String key) {
            int start = topLevelValueStart(json, key);
            if (start < 0) {
                return "";
            }
            int end = start;
            while (end < json.length()) {
                char ch = json.charAt(end);
                if ((ch >= '0' && ch <= '9') || ch == '-') {
                    end++;
                    continue;
                }
                break;
            }
            return end > start ? json.substring(start, end) : "";
        }

        private static boolean jsonBoolean(String json, String key) {
            int start = topLevelValueStart(json, key);
            return start >= 0 && json.startsWith("true", start);
        }

        private static List<String> jsonStringArray(String json, String key) {
            List<String> out = new ArrayList<>();
            int index = topLevelValueStart(json, key);
            if (index < 0 || json.charAt(index) != '[') {
                return out;
            }
            index++;
            while (index < json.length()) {
                index = skipWhitespaceAndCommas(json, index);
                if (index < 0 || index >= json.length() || json.charAt(index) == ']') {
                    return out;
                }
                ParsedString value = parseJsonString(json, index);
                if (value == null) {
                    return new ArrayList<>();
                }
                out.add(value.value);
                index = value.next;
            }
            return new ArrayList<>();
        }

        private static int parsePositiveInt(String value) {
            if (value == null || value.isEmpty()) {
                return 0;
            }
            try {
                int parsed = Integer.parseInt(value.trim());
                return parsed > 0 ? parsed : 0;
            } catch (NumberFormatException ex) {
                return 0;
            }
        }

        private static String jsonObject(String json, String key) {
            int start = topLevelValueStart(json, key);
            if (start < 0 || json.charAt(start) != '{') {
                return "";
            }
            int end = scanCompositeEnd(json, start, '{', '}');
            return end < 0 ? "" : json.substring(start, end + 1);
        }

        private static Map<String, String> jsonStringMap(String jsonObject) {
            Map<String, String> out = new LinkedHashMap<>();
            if (jsonObject == null || jsonObject.trim().isEmpty()) {
                return out;
            }
            int i = nextNonWhitespace(jsonObject, 0);
            if (i < 0 || jsonObject.charAt(i) != '{') {
                return out;
            }
            i++;
            while (i < jsonObject.length()) {
                i = skipWhitespaceAndCommas(jsonObject, i);
                if (i < 0 || i >= jsonObject.length() || jsonObject.charAt(i) == '}') {
                    return out;
                }
                ParsedString key = parseJsonString(jsonObject, i);
                if (key == null) {
                    return out;
                }
                i = skipWhitespace(jsonObject, key.next);
                if (i < 0 || i >= jsonObject.length() || jsonObject.charAt(i) != ':') {
                    return out;
                }
                i = skipWhitespace(jsonObject, i + 1);
                if (i < 0 || i >= jsonObject.length()) {
                    return out;
                }
                String value;
                if (jsonObject.charAt(i) == '"') {
                    ParsedString parsedValue = parseJsonString(jsonObject, i);
                    if (parsedValue == null) {
                        return out;
                    }
                    value = parsedValue.value;
                    i = parsedValue.next;
                } else if (jsonObject.charAt(i) == '{') {
                    int end = scanCompositeEnd(jsonObject, i, '{', '}');
                    if (end < 0) {
                        return out;
                    }
                    value = jsonObject.substring(i, end + 1);
                    i = end + 1;
                } else if (jsonObject.charAt(i) == '[') {
                    int end = scanCompositeEnd(jsonObject, i, '[', ']');
                    if (end < 0) {
                        return out;
                    }
                    value = jsonObject.substring(i, end + 1);
                    i = end + 1;
                } else {
                    int end = i;
                    while (end < jsonObject.length() && jsonObject.charAt(end) != ',' && jsonObject.charAt(end) != '}') {
                        end++;
                    }
                    value = jsonObject.substring(i, end).trim();
                    if ("null".equals(value)) {
                        value = "";
                    }
                    i = end;
                }
                out.put(key.value, value);
            }
            return out;
        }

        private static ParsedString parseJsonString(String json, int start) {
            if (start < 0 || start >= json.length() || json.charAt(start) != '"') {
                return null;
            }
            StringBuilder out = new StringBuilder();
            boolean escaped = false;
            for (int i = start + 1; i < json.length(); i++) {
                char ch = json.charAt(i);
                if (escaped) {
                    if (ch == 'n') {
                        out.append('\n');
                    } else if (ch == 'r') {
                        out.append('\r');
                    } else if (ch == 't') {
                        out.append('\t');
                    } else if (ch == 'b') {
                        out.append('\b');
                    } else if (ch == 'f') {
                        out.append('\f');
                    } else if (ch == 'u' && i + 4 < json.length()) {
                        String hex = json.substring(i + 1, i + 5);
                        try {
                            out.append((char) Integer.parseInt(hex, 16));
                            i += 4;
                        } catch (NumberFormatException ex) {
                            out.append("\\u").append(hex);
                            i += 4;
                        }
                    } else {
                        out.append(ch);
                    }
                    escaped = false;
                    continue;
                }
                if (ch == '\\') {
                    escaped = true;
                    continue;
                }
                if (ch == '"') {
                    return new ParsedString(out.toString(), i + 1);
                }
                out.append(ch);
            }
            return null;
        }

        private static int scanCompositeEnd(String json, int start, char open, char close) {
            int depth = 0;
            boolean inString = false;
            boolean escaped = false;
            for (int i = start; i < json.length(); i++) {
                char ch = json.charAt(i);
                if (inString) {
                    if (escaped) {
                        escaped = false;
                    } else if (ch == '\\') {
                        escaped = true;
                    } else if (ch == '"') {
                        inString = false;
                    }
                    continue;
                }
                if (ch == '"') {
                    inString = true;
                    continue;
                }
                if (ch == open) {
                    depth++;
                } else if (ch == close) {
                    depth--;
                    if (depth == 0) {
                        return i;
                    }
                }
            }
            return -1;
        }

        private static int nextNonWhitespace(String value, int start) {
            int i = skipWhitespace(value, start);
            if (i >= value.length()) {
                return -1;
            }
            return i;
        }

        private static int skipWhitespaceAndCommas(String value, int start) {
            int i = start;
            while (i < value.length()) {
                char ch = value.charAt(i);
                if (Character.isWhitespace(ch) || ch == ',') {
                    i++;
                    continue;
                }
                return i;
            }
            return -1;
        }

        private static int skipWhitespace(String value, int start) {
            int i = start;
            while (i < value.length() && Character.isWhitespace(value.charAt(i))) {
                i++;
            }
            return i;
        }

        private static String firstNonEmpty(String... values) {
            for (String value : values) {
                if (value != null && !value.trim().isEmpty()) {
                    return value.trim();
                }
            }
            return "";
        }

        private static final class ParsedString {
            final String value;
            final int next;

            ParsedString(String value, int next) {
                this.value = value;
                this.next = next;
            }
        }
    }

    private static final Object DIAGNOSTIC_FILE_LOCK = new Object();

    private static void appendDiagnostic(byte[] value, int offset, int length) {
        String rawPath = System.getenv("APEXLOAD_JAVA_VUSER_DIAGNOSTIC_FILE");
        if (rawPath == null || rawPath.trim().isEmpty() || length <= 0) {
            return;
        }
        synchronized (DIAGNOSTIC_FILE_LOCK) {
            try {
                Path path = Paths.get(rawPath);
                long existing = Files.exists(path) ? Files.size(path) : 0L;
                if (existing >= MAX_DIAGNOSTIC_FILE_BYTES) {
                    return;
                }
                int accepted = (int) Math.min((long) length, MAX_DIAGNOSTIC_FILE_BYTES - existing);
                Files.write(path, java.util.Arrays.copyOfRange(value, offset, offset + accepted),
                        StandardOpenOption.CREATE, StandardOpenOption.APPEND);
            } catch (IOException | RuntimeException ignored) {
                // The framed result remains authoritative. Diagnostic mirroring
                // is best-effort and must never alter user lifecycle behavior.
            }
        }
    }

    private static final class DiagnosticBuffer extends OutputStream {
        private final ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        private final int maxBytes;
        private boolean truncated;

        DiagnosticBuffer(int maxBytes) {
            this.maxBytes = maxBytes;
        }

        @Override
        public synchronized void write(int value) {
            if (bytes.size() < maxBytes) {
                bytes.write(value);
                byte[] one = new byte[]{(byte) value};
                appendDiagnostic(one, 0, 1);
            } else {
                truncated = true;
            }
        }

        @Override
        public synchronized void write(byte[] value, int offset, int length) throws IOException {
            if (value == null) {
                throw new NullPointerException("value");
            }
            if (offset < 0 || length < 0 || offset + length > value.length) {
                throw new IndexOutOfBoundsException();
            }
            int remaining = maxBytes - bytes.size();
            if (remaining > 0) {
                int accepted = Math.min(remaining, length);
                bytes.write(value, offset, accepted);
                appendDiagnostic(value, offset, accepted);
                if (accepted < length) {
                    truncated = true;
                }
            } else if (length > 0) {
                truncated = true;
            }
        }

        synchronized void reset() {
            bytes.reset();
            truncated = false;
        }

        synchronized String text() {
            return new String(bytes.toByteArray(), StandardCharsets.UTF_8);
        }

        synchronized boolean truncated() {
            return truncated;
        }
    }

}
