package apexload.host;

import java.io.*;
import java.nio.ByteBuffer;
import java.nio.charset.*;
import java.security.MessageDigest;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import java.util.*;

/** Experimental IPC v3. Direction and all identities are authenticated. */
public final class PooledWire {
    static final String CONTRACT = "apexload.java_vuser.ipc.v3";
    static final int MAX_FRAME = 1048576;
 static final int MAX_NATIVE_FRAME=3*1048576+8192;
 static int limit(String op){return "native_call".equals(op)||"native_reply".equals(op)?MAX_NATIVE_FRAME:MAX_FRAME;}

    static final class Frame {
        String generation, slot, request, operation;
        long sequence;
        final Map<String,String> fields = new TreeMap<String,String>();
        Frame reply() {
            Frame f = new Frame();
            f.generation = generation;
            f.slot = slot;
            f.request = request;
            f.operation = operation;
            f.sequence = sequence;
            return f;
        }
    }

    private static byte[] mac(byte[] payload, byte[] secret, boolean response) throws Exception {
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(secret, "HmacSHA256"));
        mac.update((byte)(response ? 1 : 0));
        return mac.doFinal(payload);
    }

    private static String string(DataInputStream in, int limit) throws IOException {
        int n = in.readInt();
        if (n < 0 || n > limit || n > in.available()) throw new IOException("pooled_frame_invalid");
        byte[] raw = new byte[n];
        in.readFully(raw);
        try {
            return StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(raw)).toString();
        } catch (CharacterCodingException e) { throw new IOException("pooled_frame_invalid"); }
    }

    private static void string(DataOutputStream out, String value) throws IOException {
        byte[] raw = value.getBytes(StandardCharsets.UTF_8);
        out.writeInt(raw.length);
        out.write(raw);
    }

    private static void validate(Frame f) throws IOException {
        if (f.generation == null || !f.generation.matches("[A-Za-z0-9_-]{1,128}") || f.slot == null
                || !f.slot.matches("[A-Za-z0-9_-]{0,128}") || f.sequence <= 0
                || !Long.toString(f.sequence).equals(f.request)
                || !Arrays.asList("ready", "open", "commit", "invoke", "interrupt", "close", "health", "native_call", "native_reply").contains(f.operation)
                || f.fields.size() > 1024) throw new IOException("pooled_frame_invalid");
        for (Map.Entry<String,String> e : f.fields.entrySet()) {
            if (e.getKey().isEmpty() || e.getKey().getBytes(StandardCharsets.UTF_8).length > 128
                    || e.getValue().getBytes(StandardCharsets.UTF_8).length > 65536)
                throw new IOException("pooled_frame_invalid");
        }
    }

    static Frame read(DataInputStream in, byte[] secret) throws Exception {
        int n = in.readInt();
        if (n < 32 || n > MAX_NATIVE_FRAME) throw new IOException("pooled_frame_invalid");
        byte[] payload = new byte[n - 32], signature = new byte[32];
        in.readFully(payload);
        in.readFully(signature);
        if (!MessageDigest.isEqual(signature, mac(payload, secret, false))) throw new IOException("pooled_frame_invalid");
        DataInputStream data = new DataInputStream(new ByteArrayInputStream(payload));
        if (!CONTRACT.equals(string(data, 128))) throw new IOException("pooled_frame_invalid");
        Frame f = new Frame();
        f.generation = string(data, 128);
        f.slot = string(data, 128);
        f.request = string(data, 128);
        f.operation = string(data, 128);
        f.sequence = data.readLong();
        int count = data.readInt();
        if (count < 0 || count > 1024) throw new IOException("pooled_frame_invalid");
        for (int i = 0; i < count; i++) {
            String k = string(data, 128), v = string(data, 65536);
            if (f.fields.put(k, v) != null) throw new IOException("pooled_frame_invalid");
        }
        if (data.available() != 0) throw new IOException("pooled_frame_invalid");
        validate(f);
 if(n>limit(f.operation)) throw new IOException("pooled_frame_invalid");
        return f;
    }

    static byte[] encode(Frame f, byte[] secret) throws Exception {
        validate(f);
        ByteArrayOutputStream buffer = new ByteArrayOutputStream();
        DataOutputStream out = new DataOutputStream(buffer);
        string(out, CONTRACT);
        string(out, f.generation);
        string(out, f.slot);
        string(out, f.request);
        string(out, f.operation);
        out.writeLong(f.sequence);
        out.writeInt(f.fields.size());
        for (Map.Entry<String,String> e : f.fields.entrySet()) {
            string(out, e.getKey());
            string(out, e.getValue());
            if (buffer.size() > limit(f.operation) - 32) throw new IOException("pooled_frame_invalid");
        }
        byte[] payload = buffer.toByteArray();
        buffer.reset();
        out.writeInt(payload.length + 32);
        out.write(payload);
        out.write(mac(payload, secret, true));
        return buffer.toByteArray();
    }
}
