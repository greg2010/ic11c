using System;

namespace PrefabReader;

/// <summary>
/// A refusal this reader decided on, printed as its message with no trace. Distinct from a
/// framework exception type so a caught one is known to be a decided refusal, not a surprise.
/// </summary>
internal sealed class RefusalException : Exception
{
    internal RefusalException(string message)
        : base(message)
    {
    }
}
