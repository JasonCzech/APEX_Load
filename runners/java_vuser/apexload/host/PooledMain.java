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
    // At least one reply slot per request the Go transport may leave outstanding
    // (max(512, 4*slots+64)), so a synchronized burst of completions queues
    // instead of overflowing into a whole-host reap.
    private final ArrayBlockingQueue<byte[]> responses;
    private final ArrayBlockingQueue<byte[]> nativeResponses = new ArrayBlockingQueue<byte[]>(4);
    // An empty frame on responses only wakes the writer; one per native permit
    // is reserved in its capacity.
    private static final byte[] WAKE = new byte[0];
    private final Semaphore nativePermits=new Semaphore(4);
    private final ConcurrentHashMap<String,NativePending> nativePending=new ConcurrentHashMap<String,NativePending>();
    private static final class NativePending {
      final PooledWire.Frame invocation; final long sequence; final CompletableFuture<String> result=new CompletableFuture<String>();
      NativePending(PooledWire.Frame frame,long sequence){this.invocation=frame;this.sequence=sequence;}
    }
    private static boolean nativeAvailable(){try {Class.forName("apexload.bridge.NativeBridge",false,PooledMain.class.getClassLoader());return true;}catch(ClassNotFoundException e){return false;}}
    private String nativeCall(PooledWire.Frame invocation,long sequence,String payload) {
      if(sequence<1||sequence>1024||payload.getBytes(StandardCharsets.UTF_8).length>1048576||!nativePermits.tryAcquire())throw new IllegalStateException("native_service_capacity");
      NativePending pending=new NativePending(invocation,sequence);
      if(nativePending.putIfAbsent(invocation.slot,pending)!=null){nativePermits.release();throw new IllegalStateException("native_service_busy");}
      try {
        PooledWire.Frame frame=invocation.reply();frame.operation="native_call";frame.fields.put("callback_sequence",Long.toString(sequence));
        String encoded=Base64.getEncoder().encodeToString(payload.getBytes(StandardCharsets.UTF_8));
        for(int i=0,pos=0;pos<encoded.length();i++,pos+=65536)frame.fields.put("payload"+i,encoded.substring(pos,Math.min(pos+65536,encoded.length())));
        if(!nativeResponses.offer(PooledWire.encode(frame,secret)))throw new IOException("native_service_capacity");
        responses.offer(WAKE); // a writer parked on responses sends this call now, not after its poll timeout
        return pending.result.get();
      } catch(InterruptedException e){Thread.currentThread().interrupt();throw new IllegalStateException("native_service_canceled");}
      catch(Exception e){throw new IllegalStateException("native_service_exchange_lost");}
      finally {nativePending.remove(invocation.slot,pending);nativePermits.release();}
    }
    private void nativeReply(PooledWire.Frame frame)throws Exception {
      NativePending pending=nativePending.get(frame.slot);
      if(pending==null||!generation.equals(frame.generation)||frame.sequence!=pending.invocation.sequence||!frame.request.equals(pending.invocation.request)||!Long.toString(pending.sequence).equals(frame.fields.get("callback_sequence"))||frame.fields.size()<2||frame.fields.size()>44)throw new IOException("native_service_reply_invalid");
      StringBuilder encoded=new StringBuilder();
      for(int i=0;i<frame.fields.size()-1;i++){String part=frame.fields.get("payload"+i);if(part==null||part.isEmpty())throw new IOException("native_service_reply_invalid");encoded.append(part);}
      byte[] raw=Base64.getDecoder().decode(encoded.toString());if(raw.length>2*1048576)throw new IOException("native_service_reply_invalid");
      String value=StandardCharsets.UTF_8.newDecoder().onMalformedInput(java.nio.charset.CodingErrorAction.REPORT).decode(java.nio.ByteBuffer.wrap(raw)).toString();
      if(!pending.result.complete(value))throw new IOException("native_service_reply_invalid");
    }
    private final Set<String> closing = Collections.newSetFromMap(new ConcurrentHashMap<String,Boolean>());
    private final Diagnostics diagnostics;
    private final GCPauses gcPauses;
    private volatile String terminalControlRequest = "";

    private PooledMain(PooledHost host, int capacity, String generation, boolean cleanup, byte[] secret, Diagnostics diagnostics) {
        this.host = host;
        this.responses = new ArrayBlockingQueue<byte[]>(Math.max(512, 4 * capacity + 64) + 4);
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

    private static boolean isSlotClosed(Throwable failure) {
        // Java 8 wraps a dependent stage's failure in CompletionException.
        while (failure instanceof java.util.concurrent.CompletionException && failure.getCause() != null) failure = failure.getCause();
        return failure instanceof IllegalStateException && "slot_closed".equals(failure.getMessage());
    }

    /** The class and message of a script's exception, bounded and on one line. */
    static String scriptError(Throwable failure) {
        Throwable cause = failure;
        while (cause instanceof java.util.concurrent.CompletionException && cause.getCause() != null) cause = cause.getCause();
        String text = cause.getClass().getName() + (cause.getMessage() == null ? "" : ": " + cause.getMessage());
        StringBuilder out = new StringBuilder();
        for (int i = 0; i < text.length() && out.length() < 2048; i++) {
            char c = text.charAt(i);
            out.append(Character.isISOControl(c) || Character.isSurrogate(c) ? ' ' : c);
        }
        return out.toString();
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
 if("native_services".equals(entry.getKey())&&"v1".equals(entry.getValue())&&nativeAvailable())continue;
                if (!entry.getKey().startsWith("p.") || entry.getKey().length() == 2)
                    throw new IOException("pooled_frame_invalid");
                parameters.put(entry.getKey().substring(2), entry.getValue());
            }
            host.invoke(generation, f.slot, f.sequence, f.request, method, parameters, "v1".equals(f.fields.get("native_services")) ? (seq,payload)->nativeCall(f,seq.longValue(),payload) : null).whenComplete((value, failure) -> {
                Map<String,String> result = new HashMap<String,String>();
                // In LoadRunner thread mode a script's exception is that VUser's
                // failed call, reported with its text, never a host failure.
                if (failure != null && host.loadRunnerThreads() && !closing.contains(f.slot) && !host.requiresReap() && !isSlotClosed(failure)) {
                    result.put("return_code", "-1");
                    result.put("event_fallback", "false");
                    result.put("event_truncated", "false");
                    result.put("error", scriptError(failure));
                    reply(f, "ok", result);
                    return;
                }
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
                if ("ready".equals(f.operation)) {status.put("event_attribution", "slot_thread_v1");if(nativeAvailable())status.put("native_data","v1");}
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
            if (dependencyCount < 0 || dependencyCount > 256 || args.length != pos + dependencyCount && args.length != pos + dependencyCount + 1)
                throw new IllegalArgumentException();
            URL[] dependencies = new URL[dependencyCount];
            for (int i = 0; i < dependencyCount; i++) dependencies[i] = local(args[pos++]);
            // An optional last argument selects LoadRunner thread mode.
            boolean threads = false;
            if (pos < args.length) {
                if (!"loadrunner_threads".equals(args[pos])) throw new IllegalArgumentException();
                threads = true;
            }

            byte[] secret = new byte[32];
            bootstrap.readFully(secret);
            byte[] proof = hostProof(generation, secret);
            Socket controlSocket = new Socket();
            // Frames are written whole; without this, a small frame that follows
            // an unacknowledged one waits for loopback's delayed ACK (~40 ms).
            controlSocket.setTcpNoDelay(true);
            controlSocket.connect(new InetSocketAddress("127.0.0.1", controlPort), 5000);
            final OutputStream output = controlSocket.getOutputStream();
            output.write(proof);
            output.flush();
            final DataInputStream control = new DataInputStream(controlSocket.getInputStream());
            PooledMain main = new PooledMain(new PooledHost(generation, capacity, shim, scripts, dependencies, target, threads),
                capacity, generation, Boolean.parseBoolean(cleanup), secret, diagnostics);
            Thread writer = new Thread(() -> {
                try {
                    for (;;) {
                      byte[] data=main.responses.poll();
                      if(data==null)data=main.nativeResponses.poll();
                      if(data==null)data=main.responses.poll(10,TimeUnit.MILLISECONDS);
                      if(data!=null&&data.length>0){output.write(data);output.flush();}
                    }
                } catch (Throwable failure) { main.fatal(); }
            }, "apexload-host-writer");
            writer.setDaemon(true);
            writer.start();
            long sequence = 0;
            for (;;) {
                PooledWire.Frame request = PooledWire.read(control, secret);
                if("native_reply".equals(request.operation)){main.nativeReply(request);continue;}
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
