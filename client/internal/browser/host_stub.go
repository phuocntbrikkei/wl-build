//go:build !windows && !(cgo && (darwin || linux))

package browser

import "errors"

func runHost(c *Core, initial string) error {
	return errors.New("trình duyệt tích hợp chưa hỗ trợ hệ điều hành này")
}
