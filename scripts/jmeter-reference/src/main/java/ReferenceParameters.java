import java.util.Random;
import org.apache.jmeter.modifiers.CounterConfig;
import org.apache.jmeter.config.RandomVariableConfig;
import org.apache.jmeter.engine.event.LoopIterationEvent;
import org.apache.jmeter.threads.JMeterContextService;
import org.apache.jmeter.threads.JMeterVariables;
import org.apache.jmeter.util.JMeterUtils;

/** Deterministic parameter vectors from the pinned JDK and JMeter classes. */
public final class ReferenceParameters {
  public static void main(String[] args) {
    if (Runtime.version().feature() != 17) throw new IllegalStateException("JDK 17 required");
    JMeterUtils.setJMeterHome(args[0]);
    JMeterUtils.loadJMeterProperties(args[0] + "/bin/jmeter.properties");
    if (!JMeterUtils.getJMeterVersion().equals("5.4.1")) throw new IllegalStateException("JMeter 5.4.1 required");
    StringBuilder json = new StringBuilder("{\"random\":[");
    boolean first = true;
    for (long seed : new long[]{-1,0,1,42,Long.MIN_VALUE,Long.MAX_VALUE}) {
      for (int bound : new int[]{1,2,6,1073741825,Integer.MAX_VALUE}) {
        if (!first) json.append(','); first = false;
        json.append("{\"seed\":").append(seed).append(",\"bound\":").append(bound).append(",\"values\":[");
        Random random = new Random(seed);
        for (int i = 0; i < 20; i++) { if (i > 0) json.append(','); json.append(random.nextInt(bound)); }
        json.append("]}");
      }
    }
    json.append("],\"jmeter_counter\":[");
    JMeterVariables vars = new JMeterVariables();
    JMeterContextService.getContext().setVariables(vars);
    CounterConfig counter = new CounterConfig();
    counter.setVarName("counter"); counter.setStart(0); counter.setEnd(5); counter.setIncrement(1); counter.setIsPerUser(false);
    for (int i = 0; i < 20; i++) {
      counter.iterationStart(new LoopIterationEvent(counter, i + 1));
      if (i > 0) json.append(','); json.append(vars.get("counter"));
    }
    json.append("],\"jmeter_random\":[");
    RandomVariableConfig random = new RandomVariableConfig();
    random.setVariableName("partition"); random.setMinimumValue("0"); random.setMaximumValue("5"); random.setRandomSeed("42"); random.setPerThread(false);
    for (int i = 0; i < 20; i++) {
      random.iterationStart(new LoopIterationEvent(random, i + 1));
      if (i > 0) json.append(','); json.append(vars.get("partition"));
    }
    System.out.println(json.append("]}"));
  }
}
