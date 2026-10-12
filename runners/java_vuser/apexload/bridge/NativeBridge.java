package apexload.bridge;

import apexload.data.Data;
import apexload.data.NativeJson;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.function.BiFunction;

/** Parent-loaded runner bridge. The authenticated transport never reaches guest APIs. */
public final class NativeBridge {
    private NativeBridge() {}
    private static final ThreadLocal<Invocation> current=new ThreadLocal<Invocation>();
    private static final class Invocation {
        final String id; final Thread thread; final BiFunction<Long,String,String> transport;long sequence;
        Invocation(String id,BiFunction<Long,String,String> transport){this.id=id;this.transport=transport;thread=Thread.currentThread();}
    }
    public static void begin(String id,BiFunction<Long,String,String> transport){
        if(current.get()!=null||id==null||id.isEmpty()||id.length()>256||transport==null)throw new Data.Failure("invocation_invalid",false);
        current.set(new Invocation(id,transport));
    }
    public static void end(){current.remove();}
    public static Map<String,Object> call(Map<String,Object> request){
        Invocation invocation=current.get();
        if(invocation==null||invocation.thread!=Thread.currentThread())throw new Data.Failure("invocation_unavailable",false);
        String payload=NativeJson.encode(request);
        if(payload.getBytes(StandardCharsets.UTF_8).length>1024*1024)throw new Data.Failure("request_too_large",false);
        if(++invocation.sequence>1024)throw new Data.Failure("call_capacity",false);
        String raw=invocation.transport.apply(Long.valueOf(invocation.sequence),payload);
        Map<String,Object> reply=NativeJson.object(raw);
        Object error=reply.get("error");
        if(error instanceof Map){Map<?,?> failure=(Map<?,?>)error;Object code=failure.get("code");throw new Data.Failure(code instanceof String?(String)code:"operation_rejected",Boolean.TRUE.equals(failure.get("outcome_unknown")));}
        Object result=reply.get("result");if(!(result instanceof Map))throw new Data.Failure("reply_invalid",true);
        @SuppressWarnings("unchecked")Map<String,Object> typed=(Map<String,Object>)result;
        return typed;
    }
}
