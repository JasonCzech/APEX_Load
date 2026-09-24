import java.io.File;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.TimeZone;
import java.util.concurrent.atomic.AtomicInteger;
import org.apache.jmeter.control.LoopController;
import org.apache.jmeter.engine.StandardJMeterEngine;
import org.apache.jmeter.sampler.DebugSampler;
import org.apache.jmeter.samplers.SampleEvent;
import org.apache.jmeter.samplers.SampleListener;
import org.apache.jmeter.save.SaveService;
import org.apache.jmeter.testelement.AbstractTestElement;
import org.apache.jmeter.testelement.TestPlan;
import org.apache.jmeter.threads.ThreadGroup;
import org.apache.jmeter.util.JMeterUtils;
import org.apache.jorphan.collections.HashTree;

/** Startup and fixture-loading evidence only. Never sends broker traffic. */
public final class ReferenceSmoke {
  private static final AtomicInteger samples = new AtomicInteger();
  public static final class Listener extends AbstractTestElement implements SampleListener {
    public void sampleOccurred(SampleEvent e) {
      if (!e.getResult().isSuccessful()) throw new AssertionError("Smoke sample failed");
      samples.incrementAndGet();
    }
    public void sampleStarted(SampleEvent e) {}
    public void sampleStopped(SampleEvent e) {}
  }
  public static void main(String[] args) throws Exception {
    if (Runtime.version().feature() != 17) throw new IllegalStateException("Reference requires JDK 17");
    JMeterUtils.setJMeterHome(args[0]);
    JMeterUtils.loadJMeterProperties(args[0] + "/bin/jmeter.properties");
    JMeterUtils.initLocale();
    if (!JMeterUtils.getJMeterVersion().equals("5.4.1")) throw new IllegalStateException("Wrong JMeter version");
    if (!org.apache.kafka.common.utils.AppInfoParser.getVersion().equals("3.3.1")) throw new IllegalStateException("Wrong Kafka version");
    for (String type : new String[] {
      "com.di.jmeter.kafka.config.KafkaProducerConfig",
      "com.di.jmeter.kafka.sampler.KafkaProducerSampler",
      "jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler",
      "jp.co.pnop.jmeter.protocol.amqp.sampler.AzAmqpMessage",
      "kg.apc.jmeter.timers.VariableThroughputTimer"
    }) Class.forName(type).getDeclaredConstructor().newInstance();
    // Loading is safe: configure/run below uses an independent local-only plan.
    if (SaveService.loadTree(new File(args[1])).size() == 0) throw new AssertionError("Empty source tree");
    SimpleDateFormat format = new SimpleDateFormat("HH:mm:ss.S");
    format.setTimeZone(TimeZone.getTimeZone("UTC"));
    for (int milliseconds : new int[] {0,1,12,123,999}) {
      String value = format.format(new Date(milliseconds));
      if (!value.equals("00:00:00." + milliseconds)) throw new AssertionError(value);
      System.out.println("millisecond=" + milliseconds + " formatted=" + value);
    }
    TestPlan testPlan = new TestPlan("Reference startup only");
    ThreadGroup threads = new ThreadGroup();
    threads.setName("local startup"); threads.setNumThreads(1); threads.setRampUp(0);
    LoopController loops = new LoopController(); loops.setLoops(1); threads.setSamplerController(loops);
    HashTree plan = new HashTree();
    HashTree contents = plan.add(testPlan);
    contents.add(new Listener()); contents.add(threads).add(new DebugSampler());
    StandardJMeterEngine engine = new StandardJMeterEngine();
    engine.configure(plan); engine.runTest();
    long deadline = System.nanoTime() + 15_000_000_000L;
    while ((engine.isActive() || samples.get() == 0) && System.nanoTime() < deadline) Thread.sleep(20);
    if (engine.isActive() || samples.get() != 1) {engine.stopTest(); throw new AssertionError("Startup did not finish exactly one local sample");}
    System.out.println("REFERENCE_STARTUP_OK; LIVE_CONFORMANCE_UNVERIFIED");
  }
}
