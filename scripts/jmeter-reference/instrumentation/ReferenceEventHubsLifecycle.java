import java.lang.instrument.*;
import java.security.ProtectionDomain;
import java.lang.reflect.Field;
import java.util.*;
import jdk.internal.org.objectweb.asm.*;
import org.apache.jmeter.samplers.SampleResult;
import org.apache.jmeter.util.JMeterUtils;
import jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler;
import jp.co.pnop.jmeter.protocol.amqp.sampler.AzAmqpMessage;
import jp.co.pnop.jmeter.protocol.amqp.sampler.AzAmqpMessages;

/** Runs the unchanged PNOP sampler with only the SDK transport boundary replaced.
 * No SDK producer constructor or send body executes; no credentials/network needed.
 */
public final class ReferenceEventHubsLifecycle {
 static final String BUILDER="com/azure/messaging/eventhubs/EventHubClientBuilder";
 static final String CLIENT="com/azure/messaging/eventhubs/EventHubProducerClient";
 static final String RESULT="org/apache/jmeter/samplers/SampleResult";
 static final String SELF="ReferenceEventHubsLifecycle";
 static final List<String> trace=new ArrayList<>();
 static final Set<String> transformed=new HashSet<>();
 static final Set<Object> producers=Collections.newSetFromMap(new IdentityHashMap<>());
 static String mode;
 public static void event(String value) { trace.add(value); }
 public static Object create() throws Exception {
  event("create");
  if (mode.equals("create_failure")) throw new IllegalStateException("synthetic create failure");
  Field field=sun.misc.Unsafe.class.getDeclaredField("theUnsafe"); field.setAccessible(true);
  Object producer=((sun.misc.Unsafe)field.get(null)).allocateInstance(Class.forName(CLIENT.replace('/','.')));
  if (!producers.add(producer)) throw new AssertionError("producer reused");
  return producer;
 }
 public static void send(Iterable<?> events) {
  int count=0; for (Object ignored:events) count++;
  event("send:"+count);
  if (mode.equals("send_failure")) throw new IllegalStateException("synthetic send failure");
  event("ack");
 }
 public static void close() {
  event("close");
  if (mode.equals("close_failure")) throw new IllegalStateException("synthetic close failure");
 }
 public static void premain(String args, Instrumentation instrumentation) {
  instrumentation.addTransformer(new ClassFileTransformer() {
   public byte[] transform(ClassLoader loader,String name,Class<?> type,ProtectionDomain domain,byte[] bytes) {
    if (!Set.of(BUILDER,CLIENT,RESULT).contains(name)) return null;
    ClassWriter writer=new ClassWriter(ClassWriter.COMPUTE_MAXS);
    new ClassReader(bytes).accept(new ClassVisitor(Opcodes.ASM8,writer) {
     public MethodVisitor visitMethod(int access,String method,String descriptor,String signature,String[] exceptions) {
      MethodVisitor out=super.visitMethod(access,method,descriptor,signature,exceptions);
      boolean build=name.equals(BUILDER)&&method.equals("buildProducerClient")&&descriptor.equals("()L"+CLIENT+";");
      boolean send=name.equals(CLIENT)&&method.equals("send")&&descriptor.equals("(Ljava/lang/Iterable;Lcom/azure/messaging/eventhubs/models/SendOptions;)V");
      boolean close=name.equals(CLIENT)&&method.equals("close")&&descriptor.equals("()V");
      if (build||send||close) {
       transformed.add(name+"."+method);
       out.visitCode();
       if (build) {
        out.visitMethodInsn(Opcodes.INVOKESTATIC,SELF,"create","()Ljava/lang/Object;",false);
        out.visitTypeInsn(Opcodes.CHECKCAST,CLIENT); out.visitInsn(Opcodes.ARETURN);
       } else {
        if (send) out.visitVarInsn(Opcodes.ALOAD,1);
        out.visitMethodInsn(Opcodes.INVOKESTATIC,SELF,send?"send":"close",send?"(Ljava/lang/Iterable;)V":"()V",false);
        out.visitInsn(Opcodes.RETURN);
       }
       out.visitMaxs(0,0); out.visitEnd(); return null;
      }
      if (name.equals(RESULT)&&Set.of("sampleStart","sampleEnd").contains(method)&&descriptor.equals("()V")) {
       transformed.add(name+"."+method);
       return new MethodVisitor(Opcodes.ASM8,out) {
        public void visitCode() {
         super.visitCode(); visitLdcInsn(method);
         visitMethodInsn(Opcodes.INVOKESTATIC,SELF,"event","(Ljava/lang/String;)V",false);
        }
       };
      }
      return out;
     }
    },0);
    return writer.toByteArray();
   }
  });
 }
 public static void main(String[] args) throws Exception {
  if (!Runtime.version().toString().equals("17.0.16+8")) throw new IllegalStateException("Pinned JDK required");
  System.setSecurityManager(new SecurityManager() {
   public void checkPermission(java.security.Permission permission) {}
   public void checkConnect(String host,int port) { throw new SecurityException("network forbidden in offline oracle"); }
   public void checkListen(int port) { throw new SecurityException("network forbidden in offline oracle"); }
   public void checkAccept(String host,int port) { throw new SecurityException("network forbidden in offline oracle"); }
  });
  JMeterUtils.setJMeterHome(args[0]); JMeterUtils.loadJMeterProperties(args[0]+"/bin/jmeter.properties");
  AzEventHubsSampler sampler=new AzEventHubsSampler();
  sampler.setName("synthetic"); sampler.setNamespaceName("synthetic.invalid");
  sampler.setAuthType(AzEventHubsSampler.AUTHTYPE_SAS); sampler.setSharedAccessKeyName("synthetic");
  sampler.setSharedAccessKey("c3ludGhldGlj"); sampler.setEventHubName("synthetic-hub");
  AzAmqpMessages messages=new AzAmqpMessages();
  for (String body:new String[]{"first","","last"}) {
   AzAmqpMessage message=new AzAmqpMessage(AzAmqpMessages.MESSAGE_TYPE_STRING); message.setMessage(body); messages.addMessage(message);
  }
  sampler.setMessages(messages);
  List<String> rows=new ArrayList<>();
  for (String scenario:new String[]{"success","success","create_failure","send_failure","close_failure"}) {
   mode=scenario; trace.clear(); SampleResult result=null; boolean escaped=false;
   try { result=sampler.sample(null); } catch (IllegalStateException expected) { escaped=true; }
   List<String> expected=switch(mode) {
    case "create_failure" -> List.of("sampleStart","create");
    case "send_failure" -> List.of("sampleStart","create","send:3","close");
    default -> List.of("sampleStart","create","send:3","ack","sampleEnd","close");
   };
   if (!trace.equals(expected)||escaped!=mode.equals("close_failure")) throw new AssertionError(mode+": "+trace);
   if (result!=null && (result.isSuccessful()!=mode.equals("success") || (!result.isSuccessful() && result.getTime()!=0))) throw new AssertionError("sample outcome mismatch");
   rows.add("{\"mode\":\""+mode+"\",\"trace\":[\""+String.join("\",\"",trace)+"\"],\"returned\":"+!escaped+",\"success\":"+(result!=null&&result.isSuccessful())+"}");
  }
  if (transformed.size()!=5 || producers.size()!=4) throw new AssertionError("instrumentation incomplete: "+transformed);
  System.out.println("{\"vectors\":["+String.join(",",rows)+"]}");
 }
}
