namespace Zoo
{
    public interface IWalker // def cs.IWalker
    {
        void Walk(); // def cs.IWalker.Walk
    }

    public class Creature // def cs.Creature
    {
        public void Breathe() { } // def cs.Creature.Breathe
    }

    // Shares a method name with Creature and is not its subtype.
    public class Minnow
    {
        public void Breathe() { } // def cs.Minnow.Breathe
    }

    public class Hound : Creature, IWalker // def cs.Hound; super Creature -> cs.Creature; super IWalker -> cs.IWalker
    {
        public void Walk() { } // def cs.Hound.Walk

        public void Bark() // def cs.Hound.Bark
        {
            this.Breathe(); // call Breathe -> cs.Creature.Breathe
            base.Breathe(); // call Breathe -> cs.Creature.Breathe
            Breathe(); // call Breathe -> cs.Creature.Breathe
        }
    }

    public struct Pose : IWalker // super IWalker -> cs.IWalker
    {
        public void Walk() { } // def cs.Pose.Walk
    }

    public interface IRunner : IWalker { } // super IWalker -> cs.IWalker

    public record Pup(int Age) : Hound, IDisposable; // super Hound -> cs.Hound; super IDisposable -> none

    public class Plant { } // def cs.Plant

    public class Tree : Plant { } // super Plant -> cs.Plant
}
