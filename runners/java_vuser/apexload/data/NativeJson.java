package apexload.data;

import java.math.BigDecimal;
import java.util.*;

/** Small bounded JSON codec for the native SDK. No application classes cross IPC. */
public final class NativeJson {
    private NativeJson() {}
    public static String encode(Object value) {
        StringBuilder out = new StringBuilder();
        write(value, out, 0, new IdentityHashMap<Object,Boolean>());
        if (out.length() > 2 * 1024 * 1024) throw new IllegalArgumentException("native_payload_too_large");
        return out.toString();
    }
    private static void write(Object value, StringBuilder out, int depth, IdentityHashMap<Object,Boolean> active) {
        if (depth > 20 || out.length() > 2 * 1024 * 1024) throw new IllegalArgumentException("native_payload_too_large");
        if (value == null) { out.append("null"); return; }
        if (value instanceof String) { quote((String)value, out); return; }
        if (value instanceof Boolean || value instanceof Integer || value instanceof Long || value instanceof Short || value instanceof Byte || value instanceof BigDecimal) { out.append(value.toString()); return; }
        if (active.put(value, Boolean.TRUE) != null) throw new IllegalArgumentException("native_payload_cycle");
        try {
            if (value instanceof Map) {
                Map<?,?> map = (Map<?,?>)value;
                if (map.size() > 2048) throw new IllegalArgumentException("native_payload_too_large");
                out.append('{'); boolean comma = false;
                for (Map.Entry<?,?> entry : map.entrySet()) {
                    if (!(entry.getKey() instanceof String)) throw new IllegalArgumentException("native_object_key_invalid");
                    if (comma) out.append(','); comma = true;
                    quote((String)entry.getKey(), out); out.append(':'); write(entry.getValue(), out, depth + 1, active);
                }
                out.append('}');
            } else if (value instanceof List) {
                List<?> list = (List<?>)value;
                if (list.size() > 2048) throw new IllegalArgumentException("native_payload_too_large");
                out.append('['); boolean comma = false;
                for (Object child : list) { if (comma) out.append(','); comma = true; write(child,out,depth+1,active); }
                out.append(']');
            } else throw new IllegalArgumentException("native_value_type_invalid");
        } finally { active.remove(value); }
    }
    private static void quote(String text, StringBuilder out) {
        if(text.length()>2*1024*1024)throw new IllegalArgumentException("native_payload_too_large");
        out.append('"');
        for (int i=0; i<text.length(); i++) {
            char c=text.charAt(i);
            if (c=='"'||c=='\\') out.append('\\').append(c);
            else if (c<32) { out.append("\\u"); String h=Integer.toHexString(c); for(int n=h.length();n<4;n++)out.append('0');out.append(h); }
            else if (Character.isHighSurrogate(c)) { if (i+1>=text.length()||!Character.isLowSurrogate(text.charAt(i+1))) throw new IllegalArgumentException("native_unicode_invalid"); out.append(c).append(text.charAt(++i)); }
            else if (Character.isLowSurrogate(c)) throw new IllegalArgumentException("native_unicode_invalid");
            else out.append(c);
        }
        out.append('"');
    }
    public static Map<String,Object> object(String text) {
        if(text==null||text.length()>2*1024*1024)throw new IllegalArgumentException("native_payload_too_large");
        Parser p=new Parser(text); Object value=p.value(0);p.space();
        if(p.pos!=text.length()||!(value instanceof Map))throw new IllegalArgumentException("native_reply_invalid");
        @SuppressWarnings("unchecked") Map<String,Object> result=(Map<String,Object>)value;
        return result;
    }
    private static final class Parser {
        final String text; int pos, nodes;
        Parser(String text){this.text=text;}
        void space(){while(pos<text.length()&&" \r\n\t".indexOf(text.charAt(pos))>=0)pos++;}
        IllegalArgumentException fail(){return new IllegalArgumentException("native_reply_invalid");}
        Object value(int depth){
            space();if(depth>20||++nodes>524288||pos>=text.length())throw fail();char c=text.charAt(pos);
            if(c=='"')return string();
            if(c=='{'){
                pos++;Map<String,Object> m=new LinkedHashMap<String,Object>();space();if(take('}'))return Collections.unmodifiableMap(m);
                do{space();if(pos>=text.length()||text.charAt(pos)!='"')throw fail();String key=string();space();if(!take(':')||m.containsKey(key))throw fail();m.put(key,value(depth+1));space();if(take('}'))return Collections.unmodifiableMap(m);}while(take(','));throw fail();
            }
            if(c=='['){pos++;List<Object> a=new ArrayList<Object>();space();if(take(']'))return Collections.unmodifiableList(a);do{a.add(value(depth+1));space();if(take(']'))return Collections.unmodifiableList(a);}while(take(','));throw fail();}
            for(String word:new String[]{"true","false","null"})if(text.startsWith(word,pos)){pos+=word.length();return "null".equals(word)?null:Boolean.valueOf(word);}
            int start=pos;while(pos<text.length()&&"-+0123456789.eE".indexOf(text.charAt(pos))>=0)pos++;
            if(pos==start)throw fail();String number=text.substring(start,pos);if(!number.matches("-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?"))throw fail();return new BigDecimal(number);
        }
        boolean take(char c){if(pos<text.length()&&text.charAt(pos)==c){pos++;return true;}return false;}
        String string(){
            if(!take('"'))throw fail();StringBuilder s=new StringBuilder();
            while(pos<text.length()){
                char c=text.charAt(pos++);if(c=='"')return s.toString();if(c<32)throw fail();
                if(c=='\\'){
                    if(pos>=text.length())throw fail();c=text.charAt(pos++);
                    switch(c){case '"':case '\\':case '/':s.append(c);break;case 'b':s.append('\b');break;case 'f':s.append('\f');break;case 'n':s.append('\n');break;case 'r':s.append('\r');break;case 't':s.append('\t');break;case 'u':if(pos+4>text.length())throw fail();try{s.append((char)Integer.parseInt(text.substring(pos,pos+4),16));}catch(NumberFormatException e){throw fail();}pos+=4;break;default:throw fail();}
                }else s.append(c);
            }throw fail();
        }
    }
}
