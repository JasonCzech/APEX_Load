package apexload.host;

import java.io.*;
import java.net.URL;
import java.net.Socket;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.*;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/** Internal experimental runner entry point. An external owner MUST enforce the
 * run lease and reap strong containment, including after this JVM exits. */
public final class PooledMain {
    private final PooledHost host;
    private final String generation;
    private final boolean qualifiedCleanup;
    private final byte[] secret;
    private final ArrayBlockingQueue<byte[]> responses = new ArrayBlockingQueue<byte[]>(64);
    private final Set<String> closing = Collections.newSetFromMap(new ConcurrentHashMap<String,Boolean>());
    private final Diagnostics diagnostics;
    private final GCPauses gcPauses;
    private volatile String terminalControlRequest = "";

    private PooledMain(PooledHost host, String generation, boolean cleanup, byte[] secret, Diagnostics diagnostics) {
        this.host = host;
        this.generation = generation;
        this.qualifiedCleanup = cleanup;
        this.secret = secret;
        this.diagnostics = diagnostics;
        this.gcPauses = GCPauses.start();
    }

    private static final class Diagnostics extends OutputStream {
        private final ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        private long dropped;
        public synchronized void write(int b) {
            if (bytes.size() < 32768) bytes.write(b);
            else if (dropped < Long.MAX_VALUE) dropped++;
        }
        public synchronized void write(byte[] b, int off, int len) {
            int n = Math.min(len, 32768 - bytes.size());
            bytes.write(b, off, n);
            dropped += Math.min(Long.MAX_VALUE - dropped, (long)len - n);
        }
        synchronized String snapshot() { return Base64.getEncoder().encodeToString(bytes.toByteArray()); }
        synchronized long dropped() { return dropped; }
    }

    private void reply(PooledWire.Frame request, String code, Map<String,String> fields) {
        try {
            // Interrupting peers can finish their futures before the triggering
            // callback runs. Only the original terminal response may retire the
            // transport; peers are completed by the Go owner as host-reaped.
            boolean reap = host.requiresReap();
            if (reap) {
                String origin = host.reapRequest();
                if (origin.isEmpty()) origin = terminalControlRequest;
                if (!origin.equals(request.request)) return;
            }
            PooledWire.Frame response = request.reply();
            response.fields.putAll(fields);
            response.fields.put("code", code);
            response.fields.put("reap", Boolean.toString(reap));
            // Never wait behind a blocked output socket on a slot/control thread.
            if (!responses.offer(PooledWire.encode(response, secret))) fatal();
        } catch (Throwable failure) { fatal(); }
    }

    private void fatal() {
        host.requestReap();
        Runtime.getRuntime().halt(70);
    }

    private void dispatch(final PooledWire.Frame f) throws Exception {
        if (!generation.equals(f.generation)) throw new IOException("stale_host_generation");
        final Map<String,String> empty = Collections.emptyMap();
        boolean slotOp = Arrays.asList("open", "invoke", "interrupt", "close").contains(f.operation);
        if (slotOp == f.slot.isEmpty()) throw new IOException("pooled_frame_invalid");

        if ("open".equals(f.operation)) {
            for (String key : f.fields.keySet()) {
                if (!Arrays.asList("APEXLOAD_VUSER_ID", "APEXLOAD_GROUP_NAME", "APEXLOAD_SCENARIO_ID",
                        "APEXLOAD_IGNORE_THINK_TIME", "APEXLOAD_THINK_TIME_LIMIT_MS",
                        "APEXLOAD_JAVA_VUSER_EVENT_FILE").contains(key)) throw new IOException("pooled_frame_invalid");
            }
            host.prepareSlot(generation, f.slot, f.fields);
            reply(f, "ok", empty);
        } else if ("invoke".equals(f.operation)) {
            String method = f.fields.get("method");
            Map<String,String> parameters = new HashMap<String,String>();
            for (Map.Entry<String,String> entry : f.fields.entrySet()) {
                if ("method".equals(entry.getKey())) continue;
                if (!entry.getKey().startsWith("p.") || entry.getKey().length() == 2)
                    throw new IOException("pooled_frame_invalid");
                parameters.put(entry.getKey().substring(2), entry.getValue());
            }
            host.invoke(generation, f.slot, f.sequence, f.request, method, parameters).whenComplete((value, failure) -> {
                Map<String,String> result = new HashMap<String,String>();
                if (failure == null) {
                    result.put("return_code", Integer.toString(value.returnCode));
                    result.put("event_fallback", Boolean.toString(value.eventFallback));
                    result.put("event_truncated", Boolean.toString(value.eventTruncated));
                    if (value.exit != null) {
                        result.put("exit_option", Integer.toString(value.exit.option));
                        result.put("exit_status", Integer.toString(value.exit.status));
                    }
                }
                String code = failure == null ? "ok" : closing.contains(f.slot) && !host.requiresReap()
                    ? "slot_closed" : "pooled_invocation_failed";
                reply(f, code, result);
            });
        } else {
            if (!f.fields.isEmpty()) throw new IOException("pooled_frame_invalid");
            if ("commit".equals(f.operation)) {
                host.commit(generation);
                reply(f, "ok", empty);
            } else if ("close".equals(f.operation) || "interrupt".equals(f.operation)) {
                closing.add(f.slot);
                if (!qualifiedCleanup) {
                    host.closeSlot(generation, f.slot, false, f.request);
                    reply(f, "pooled_cleanup_uncertain", empty);
                } else {
                    host.closeSlot(generation, f.slot, true, f.request).whenComplete((ignored, failure) -> {
                        reply(f, failure == null ? "ok" : "pooled_cleanup_uncertain", empty);
                    });
                }
            } else {
                Map<String,String> status = new HashMap<String,String>();
                status.put("sealed", Boolean.toString(host.isSealed()));
                if ("ready".equals(f.operation)) status.put("event_attribution", "slot_thread_v1");
                if ("health".equals(f.operation)) {
                    status.put("diagnostics_base64", diagnostics.snapshot());
                    status.put("diagnostics_dropped", Long.toString(diagnostics.dropped()));
                    gcPauses.fields(status);
                }
                reply(f, "ok", status);
            }
        }
    }

    private static URL local(String path) throws Exception { return new File(path).toURI().toURL(); }

    private static byte[] hostProof(String generation, byte[] secret) throws Exception {
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(secret, "HmacSHA256"));
        return mac.doFinal(("apexload.java_vuser.ipc.v3.host_auth:" + generation).getBytes(StandardCharsets.UTF_8));
    }

    public static void main(String[] args) throws Exception {
        // Bootstrap contains no user code. Stdin carries only the host secret;
        // the owner closes fd 0 before any slot can run. Subsequent requests
        // and replies share one authenticated duplex socket.
        final DataInputStream bootstrap = new DataInputStream(System.in);
        // Raw/native stdout and stderr remain supervisor-owned diagnostic streams.
        // Protocol traffic uses a socket so FileDescriptor/native stream access
        // cannot consume or spoof framing.
        final Diagnostics diagnostics = new Diagnostics();
        System.setIn(new ByteArrayInputStream(new byte[0]));
        System.setOut(new PrintStream(diagnostics, true, "UTF-8"));
        System.setErr(new PrintStream(diagnostics, true, "UTF-8"));
        try {
            if (args.length < 9) throw new IllegalArgumentException("invalid host configuration");
            int pos = 0;
            String generation = args[pos++];
            int capacity = Integer.parseInt(args[pos++]);
            URL shim = local(args[pos++]);
            String target = args[pos++];
            String cleanup = args[pos++];
            if (!"true".equals(cleanup) && !"false".equals(cleanup)) throw new IllegalArgumentException();
            int controlPort = Integer.parseInt(args[pos++]);
            if (controlPort < 1 || controlPort > 65535) throw new IllegalArgumentException();
            int scriptCount = Integer.parseInt(args[pos++]);
            if (scriptCount < 1 || scriptCount > 256 || args.length < pos + scriptCount + 1)
                throw new IllegalArgumentException();
            URL[] scripts = new URL[scriptCount];
            for (int i = 0; i < scriptCount; i++) scripts[i] = local(args[pos++]);
            int dependencyCount = Integer.parseInt(args[pos++]);
            if (dependencyCount < 0 || dependencyCount > 256 || args.length != pos + dependencyCount)
                throw new IllegalArgumentException();
            URL[] dependencies = new URL[dependencyCount];
            for (int i = 0; i < dependencyCount; i++) dependencies[i] = local(args[pos++]);

            byte[] secret = new byte[32];
            bootstrap.readFully(secret);
            byte[] proof = hostProof(generation, secret);
            Socket controlSocket = new Socket();
            controlSocket.connect(new InetSocketAddress("127.0.0.1", controlPort), 5000);
            final OutputStream output = controlSocket.getOutputStream();
            output.write(proof);
            output.flush();
            final DataInputStream control = new DataInputStream(controlSocket.getInputStream());
            PooledMain main = new PooledMain(new PooledHost(generation, capacity, shim, scripts, dependencies, target),
                generation, Boolean.parseBoolean(cleanup), secret, diagnostics);
            Thread writer = new Thread(() -> {
                try {
                    for (;;) { output.write(main.responses.take()); output.flush(); }
                } catch (Throwable failure) { main.fatal(); }
            }, "apexload-host-writer");
            writer.setDaemon(true);
            writer.start();
            long sequence = 0;
            for (;;) {
                PooledWire.Frame request = PooledWire.read(control, secret);
                if (request.sequence <= sequence) throw new IOException("stale_request");
                sequence = request.sequence;
                try { main.dispatch(request); }
                catch (IllegalStateException | IllegalArgumentException invalid) {
                    main.terminalControlRequest = request.request;
                    main.host.requestReap();
                    main.reply(request, "pooled_operation_rejected", Collections.emptyMap());
                }
            }
        } catch (Throwable failure) { Runtime.getRuntime().halt(70); }
    }
}
