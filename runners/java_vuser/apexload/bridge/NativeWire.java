package apexload.bridge;

import java.io.*;
import java.nio.ByteBuffer;
import java.nio.charset.*;
import java.security.MessageDigest;
import java.util.*;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/** Authenticated, direction-bound native callback frame shared by runner modes. */
public final class NativeWire {
    public static final String CONTRACT="apexload.native_data.v1";
    public static final String PREFIX="APEXLOAD_NATIVE_DATA_V1 ";
    private static final int MAX=2*1024*1024;
    private NativeWire(){}
    public static String encode(byte[] secret,String scope,String invocation,long sequence,String payload,boolean reply)throws Exception{
        byte[] bytes=payload.getBytes(StandardCharsets.UTF_8);
        if(secret.length!=32||scope.length()>256||invocation.length()>256||sequence<=0||bytes.length>MAX)throw new IOException("native_frame_invalid");
        ByteArrayOutputStream out=new ByteArrayOutputStream();DataOutputStream d=new DataOutputStream(out);
        d.writeByte(reply?1:0);string(d,CONTRACT);string(d,scope);string(d,invocation);d.writeLong(sequence);d.writeInt(bytes.length);d.write(bytes);d.flush();
        byte[] body=out.toByteArray();Mac mac=Mac.getInstance("HmacSHA256");mac.init(new SecretKeySpec(secret,"HmacSHA256"));out.write(mac.doFinal(body));
        return Base64.getEncoder().encodeToString(out.toByteArray());
    }
    public static String decode(byte[] secret,String scope,String invocation,long sequence,String encoded,boolean reply)throws Exception{
        if(encoded==null||encoded.length()>3*1024*1024)throw new IOException("native_frame_invalid");
        byte[] raw=Base64.getDecoder().decode(encoded);if(raw.length<32||raw.length>MAX+2048)throw new IOException("native_frame_invalid");
        byte[] body=Arrays.copyOf(raw,raw.length-32);Mac mac=Mac.getInstance("HmacSHA256");mac.init(new SecretKeySpec(secret,"HmacSHA256"));
        if(!MessageDigest.isEqual(mac.doFinal(body),Arrays.copyOfRange(raw,raw.length-32,raw.length)))throw new IOException("native_frame_invalid");
        DataInputStream d=new DataInputStream(new ByteArrayInputStream(body));
        if(d.readUnsignedByte()!=(reply?1:0)||!CONTRACT.equals(string(d,128))||!scope.equals(string(d,256))||!invocation.equals(string(d,256))||sequence!=d.readLong())throw new IOException("native_frame_invalid");
        String payload=string(d,MAX);if(d.available()!=0)throw new IOException("native_frame_invalid");return payload;
    }
    private static void string(DataOutputStream d,String value)throws Exception{byte[] bytes=value.getBytes(StandardCharsets.UTF_8);d.writeInt(bytes.length);d.write(bytes);}
    private static String string(DataInputStream d,int max)throws Exception{int n=d.readInt();if(n<0||n>max||n>d.available())throw new IOException("native_frame_invalid");byte[] bytes=new byte[n];d.readFully(bytes);return StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT).onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(bytes)).toString();}
    public static String readLine(BufferedReader reader)throws IOException{
        StringBuilder line=new StringBuilder();int c;
        while((c=reader.read())!=-1){if(c=='\n')return line.toString();if(line.length()>=3*1024*1024+128)throw new IOException("native_frame_too_large");line.append((char)c);}
        throw new EOFException("native_channel_closed");
    }
}
