public class PodContainmentActions {
 public int init() throws Exception {
  java.io.File scratch = java.io.File.createTempFile("apexload", ".scratch");
  if (!scratch.delete()) throw new Exception("scratch cleanup failed");
  String endpoint = lrapi.lr.eval_string("{Target}");
  String command = "setsid sh -c 'while :; do wget -q -O /dev/null " + endpoint + "; sleep 0.1; done' >/scratch/pulses.log 2>&1 &";
  if (new ProcessBuilder("/bin/sh", "-c", command).start().waitFor()!=0) throw new Exception("descendant launch failed");
  return 0;
 }
 public int action() { while (true) { try { Thread.sleep(60000); } catch (InterruptedException ignored) {} } }
 public int end() { return 0; }
}
