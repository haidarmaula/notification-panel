//go:build darwin

package requeue

import (
	"encoding/binary"
	"runtime"
	"syscall"
	"unsafe"
)

// noExtendedACL rejects all Darwin extended security ACLs, whose write/delete/
// security grants are not represented by POSIX mode bits. getattrlist's fixed
// header contains a total length and an attrreference_t for the requested ACL.
// Only the exact empty-ACL result is accepted; errors/truncation fail closed.
// ABI: Darwin sys/attr.h ATTR_CMN_EXTENDED_SECURITY and FSOPT_NOFOLLOW.
func noExtendedACL(path string) bool {
	name, err := syscall.BytePtrFromString(path)
	if err != nil {
		return false
	}
	attributes := struct {
		Count, Reserved                       uint16
		Common, Volume, Directory, File, Fork uint32
	}{Count: 5, Common: 0x00400000}
	var header [12]byte
	_, _, errno := syscall.Syscall6(syscall.SYS_GETATTRLIST, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&header[0])), uintptr(len(header)), 1, 0)
	runtime.KeepAlive(name)
	return errno == 0 && binary.LittleEndian.Uint32(header[:4]) == 12 && binary.LittleEndian.Uint32(header[8:12]) == 0
}
