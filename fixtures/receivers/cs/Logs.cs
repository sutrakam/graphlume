namespace Logs
{
    // A static class named for what it does. A variable called `log` is not an
    // instance of it: serilog bound `log.Warning(...)` on an ILogger to
    // Log.Warning at medium, 12 times in 21.
    public static class Log // def cs.Log
    {
        public static void Warning(string message) { } // def cs.Log.Warning
    }

    public struct Inline // def cs.Inline
    {
        public int AsSpan(int count) { return count; } // def cs.Inline.AsSpan
    }

    public class Writer // def cs.Writer
    {
        public void Emit(object log, int[] values)
        {
            log.Warning("x"); // call Warning -> cs.Log.Warning low
            var span = values.AsSpan(); // call AsSpan -> none
        }
    }
}
