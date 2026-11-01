package store

import (
	"fmt"
	"strings"
)

const alphabet = "FcwAPNKTMug3GV5Lj7EJnHpWsx4tb8haYeviqBz6rkCy12mUSDQX9RdoZf"

func AV2BV(aid int64) string {
	b := []byte("BV1000000000")
	v := (uint64(1)<<51 | uint64(aid)) ^ 23442827791579
	for i := 11; v > 0 && i >= 0; i-- {
		b[i] = alphabet[v%58]
		v /= 58
	}
	b[3], b[9] = b[9], b[3]
	b[4], b[7] = b[7], b[4]
	return string(b)
}
func BV2AV(s string) (int64, error) {
	if len(s) != 12 || !strings.HasPrefix(s, "BV1") {
		return 0, fmt.Errorf("invalid bvid")
	}
	b := []byte(s)
	b[3], b[9] = b[9], b[3]
	b[4], b[7] = b[7], b[4]
	var v uint64
	for _, c := range b[3:] {
		i := strings.IndexByte(alphabet, c)
		if i < 0 {
			return 0, fmt.Errorf("invalid bvid")
		}
		v = v*58 + uint64(i)
	}
	return int64((v & 2251799813685247) ^ 23442827791579), nil
}
