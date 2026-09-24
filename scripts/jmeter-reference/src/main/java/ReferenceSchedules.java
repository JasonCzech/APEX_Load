import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.util.List;
import java.util.ArrayList;
import org.apache.jmeter.testelement.property.CollectionProperty;
import org.apache.jmeter.util.JMeterUtils;
import kg.apc.jmeter.timers.VariableThroughputTimer;

/** Pure rate/delay oracle. Never calls delay(), stopTest(), or a broker. */
public final class ReferenceSchedules {
 public static void main(String[] args) throws Exception {
  if (Runtime.version().feature() != 17) throw new IllegalStateException("JDK 17 required");
  JMeterUtils.setJMeterHome(args[0]);
  JMeterUtils.loadJMeterProperties(args[0]+"/bin/jmeter.properties");
  if (!JMeterUtils.getJMeterVersion().equals("5.4.1")) throw new IllegalStateException("JMeter 5.4.1 required");
  VariableThroughputTimer timer = new VariableThroughputTimer();
  StringBuilder out = new StringBuilder("{\"rates\":[");
  boolean first = true;
  for (double peak : new double[]{84,42}) {
   timer.setData(new CollectionProperty("load_profile", new ArrayList<>(List.of(new ArrayList<>(List.of(1.0,peak,300)),new ArrayList<>(List.of(peak,peak,3600))))));
   for (int second=0; second<=3901; second++) {
    if (!first) out.append(','); first=false;
    out.append('[').append(peak).append(',').append(second).append(',').append(timer.getRPSForSecond(second).getLeft()).append(']');
   }
  }
  Method delay = VariableThroughputTimer.class.getDeclaredMethod("getDelay",long.class); delay.setAccessible(true);
  Field sent = VariableThroughputTimer.class.getDeclaredField("cntSent"); sent.setAccessible(true);
  Field waiting = VariableThroughputTimer.class.getDeclaredField("cntDelayed"); waiting.setAccessible(true);
  Field rate = VariableThroughputTimer.class.getDeclaredField("rps"); rate.setAccessible(true);
  Field interval = VariableThroughputTimer.class.getDeclaredField("msecPerReq"); interval.setAccessible(true);
  out.append("],\"delays\":["); first=true;
  for (double r : new double[]{0.00000001,0.5,1,1.2766666666666666,42,84,10000000}) {
   rate.setDouble(timer,r); interval.setDouble(timer,1000/r);
   for (long n : new long[]{0,1,42,100}) for (int w : new int[]{0,1,50,100}) for(long ms : new long[]{0,1,499,999}) {
    sent.setLong(timer,n); waiting.setInt(timer,w);
    if (!first) out.append(','); first=false;
    out.append('[').append(r).append(',').append(n).append(',').append(w).append(',').append(ms).append(',').append(delay.invoke(timer,ms)).append(']');
   }
  }
  System.out.println(out.append("]}"));
 }
}
