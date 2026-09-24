import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.util.Base64;

/** Local-only Kafka 3.3.1 callback wire formatter oracle; no token requests. */
public final class ReferenceOAuth {
 public static void main(String[] args) throws Exception {
  if (!Runtime.version().toString().equals("17.0.16+8")) throw new IllegalStateException("Pinned JDK required");
  Class<?> callback = Class.forName("org.apache.kafka.common.security.oauthbearer.secured.HttpAccessTokenRetriever");
  Method auth = callback.getDeclaredMethod("formatAuthorizationHeader",String.class,String.class);
  Method body = callback.getDeclaredMethod("formatRequestBody",String.class);
  auth.setAccessible(true); body.setAccessible(true);
  String[][] cases = {
   {"client","secret","scope"},
   {"\u00a0client\u00a0","\u00a0secret\u00a0","\u00a0scope*~\u00a0"},
   {" client:id "," secret/? "," scope with spaces "},
   {"client+id","s&=cret","scope+plus"},
   {"café","秘密","范围"},
   {"client\tid","secret\nvalue",""},
   {"client","secret",null}
  };
  System.out.print("{\"vectors\":[");
  boolean first = true;
  for (String[] c: cases) {
   if (!first) System.out.print(","); first=false;
   String header = (String)auth.invoke(null,c[0],c[1]);
   String form = (String)body.invoke(null,c[2]);
   System.out.print("[\""+enc(c[0])+"\",\""+enc(c[1])+"\",\""+enc(c[2]==null?"":c[2])+"\",\""+header+"\",\""+enc(form)+"\"]");
  }
  System.out.println("]}");
 }
 static String enc(String v) {return Base64.getEncoder().encodeToString(v.getBytes(StandardCharsets.UTF_8));}
}
