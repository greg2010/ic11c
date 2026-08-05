package ic10

import "hash/crc32"

// HashName is UnityEngine.Animator.StringToHash, the HASH("...")
// preprocessor form, and the hash the game stamps into prefab and device
// names: CRC-32/ISO-HDLC reinterpreted as a signed 32-bit integer, native
// to the Unity runtime and absent from the decompiled assembly. A batch
// instruction selects on the hash, never the name, so a seeded device and
// the program reading it must compute it identically.
func HashName(name string) int {
	return int(int32(crc32.ChecksumIEEE([]byte(name))))
}
