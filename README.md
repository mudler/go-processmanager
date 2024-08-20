# go-processmanager

Updated version for 2024

* Updated gopsutils to v4
  * bumps module target to golang 1.18+
* Allows setting working directory
* _Breaking Change_ `config.KillSignal` is now an `*int` - avoids the need to parse at runtime. `options.WithKillSignal` will need to be updated.