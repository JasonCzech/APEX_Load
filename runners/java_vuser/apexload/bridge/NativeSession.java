package apexload.bridge;

import apexload.data.Data;
import java.io.*;
import java.util.function.BiFunction;

/** Dedicated-runner stdio callback binding. Guest code never receives these streams. */
public final class NativeSession {
    private NativeSession(){}
    public static void begin(final String invocation,final byte[] secret,final BufferedReader input,final PrintStream output){
        NativeBridge.begin(invocation,new BiFunction<Long,String,String>(){
            public String apply(Long sequence,String payload){
                try{
                    output.println(NativeWire.PREFIX+NativeWire.encode(secret,"dedicated",invocation,sequence.longValue(),payload,false));output.flush();
                    String line=NativeWire.readLine(input);
                    if(!line.startsWith(NativeWire.PREFIX))throw new IOException("native_frame_invalid");
                    return NativeWire.decode(secret,"dedicated",invocation,sequence.longValue(),line.substring(NativeWire.PREFIX.length()),true);
                }catch(Exception error){throw new Data.Failure("native_channel_lost",true);}
            }
        });
    }
    public static void end(){NativeBridge.end();}
}
