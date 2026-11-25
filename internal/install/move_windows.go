//go:build windows

package install

import "errors"

// OutsideApplications is never true on Windows: there is no app bundle.
func OutsideApplications() (string, bool) { return "", false }

func MoveToApplications(string) (string, error) { return "", errors.New("not on Windows") }

func Relaunch(string) error { return errors.New("not on Windows") }
