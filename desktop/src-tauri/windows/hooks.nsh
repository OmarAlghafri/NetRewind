; NSIS installer hooks (Tauri bundler, per-machine install).
;
; The recorder ships inside the desktop installer as a resource. After the
; files are in place it is registered as the netrewindd Windows service
; (LocalSystem, automatic start, the installing user granted access to the
; API pipe); before the files are removed it is unregistered. The event
; store and configuration under %ProgramData%\NetRewind are left in place
; on uninstall, on purpose: the record outlives the program.

!macro NSIS_HOOK_POSTINSTALL
  DetailPrint "Registering the NetRewind recorder service"
  nsExec::ExecToLog '"$INSTDIR\recorder\netrewindd.exe" service install'
  Pop $0
  ${If} $0 != 0
    DetailPrint "The recorder service was not registered (exit $0). Run 'netrewindd service install' as administrator to retry."
  ${EndIf}
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  DetailPrint "Unregistering the NetRewind recorder service"
  nsExec::ExecToLog '"$INSTDIR\recorder\netrewindd.exe" service uninstall'
  Pop $0
!macroend
