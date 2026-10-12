package apexload.data;

import apexload.bridge.NativeBridge;
import java.util.*;

/** Native APEX data API. Values are literal and 64-bit metadata uses decimal strings. */
public final class Data {
    private Data() {}
    public static Table table(String alias) { return new Table(alias); }
    public static Result call(Map<String,Object> request) { return new Result(NativeBridge.call(request)); }
    public static final class Failure extends RuntimeException {
        public final String code;
        public final boolean outcomeUnknown;
        public Failure(String code, boolean unknown){super("native data service: "+code);this.code=code;this.outcomeUnknown=unknown;}
    }
    public static final class Table {
        private final String alias;
        private Table(String alias){if(alias==null||!alias.matches("[A-Za-z_][A-Za-z0-9_-]{0,63}"))throw new IllegalArgumentException("alias_invalid");this.alias=alias;}
        public Result call(String operation, Map<String,Object> arguments){Map<String,Object> q=new LinkedHashMap<String,Object>();if(arguments!=null)q.putAll(arguments);q.put("alias",alias);q.put("operation",operation);return Data.call(q);}
        public Result push(Map<String,String> values){Map<String,Object> q=new LinkedHashMap<String,Object>();q.put("values",new LinkedHashMap<String,String>(values));return call("push",q);}
        public Result next(){return call("next",null);}
        public Result pop(){return call("pop",null);}
        public Result lease(){return call("lease",null);}
        public Result lease(int waitMS,int leaseMS){Map<String,Object> q=new LinkedHashMap<String,Object>();q.put("wait_ms",waitMS);q.put("lease_ms",leaseMS);return call("lease",q);}
        public Result ack(String reference){return control("ack",reference);}
        public Result release(String reference){return control("release",reference);}
        public Result renew(String reference){return control("renew",reference);}
        private Result control(String operation,String reference){Map<String,Object> q=new LinkedHashMap<String,Object>();q.put("lease_ref",reference);return call(operation,q);}
        public Result read(String record){Map<String,Object> q=new LinkedHashMap<String,Object>();q.put("record_id",record);return call("read",q);}
        public Result update(String record,String version,Map<String,String> values){Map<String,Object> q=new LinkedHashMap<String,Object>();q.put("record_id",record);q.put("expected_version",version);q.put("values",new LinkedHashMap<String,String>(values));return call("update",q);}
        public Result increment(String record,String column,String delta){Map<String,Object> q=new LinkedHashMap<String,Object>();q.put("record_id",record);q.put("columns",Collections.singletonList(column));q.put("delta",delta);return call("increment",q);}
    }
    public static final class Result {
        private final Map<String,Object> fields;
        private Result(Map<String,Object> fields){this.fields=fields;}
        public String status(){return text("status");}
        public String revision(){return text("revision");}
        public String count(){return text("count");}
        public String leaseRef(){return text("lease_ref");}
        public Row row(){Object row=fields.get("row");if(!(row instanceof Map))return null;@SuppressWarnings("unchecked")Map<String,Object> value=(Map<String,Object>)row;return new Row(value);}
        public Map<String,Object> fields(){return fields;}
        private String text(String name){Object value=fields.get(name);return value instanceof String?(String)value:"";}
    }
    public static final class Row {
        private final Map<String,Object> fields;
        private Row(Map<String,Object> fields){this.fields=fields;}
        public String id(){return (String)fields.get("id");}
        public String version(){return (String)fields.get("version");}
        public Map<String,String> values(){Map<String,String> result=new LinkedHashMap<String,String>();Object values=fields.get("values");if(values instanceof Map)for(Map.Entry<?,?> e:((Map<?,?>)values).entrySet())result.put((String)e.getKey(),(String)e.getValue());return Collections.unmodifiableMap(result);}
    }
}
