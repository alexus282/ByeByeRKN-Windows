package install

import "syscall"

// sysprocHide keeps console helpers (icacls, powershell, cmd) from flashing a window.
var sysprocHide = syscall.SysProcAttr{HideWindow: true}
