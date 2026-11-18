# Validación de la entrega

Fecha: 15 de noviembre de 2026. Entorno disponible: macOS arm64, Go 1.27.3.

## Verificado localmente

- `go test -race -count=1 ./...`: todos los paquetes pasan, sin carreras detectadas.
- `go vet ./...`: sin observaciones.
- `go mod tidy -diff`: sin cambios pendientes.
- Protocolo Browser simulado con intercambio NaCl, respuesta cifrada, nonce incrementado, notificaciones intercaladas, espera de desbloqueo y rechazo de nonce incorrecto.
- Cancelación de una conexión Browser sin respuesta; cierre del proxy mientras espera el desbloqueo.
- Solicitudes concurrentes con un solo desbloqueo, carga asíncrona de llave y firmas verificadas criptográficamente.
- Llave esperada ausente entre otras llaves, recuperación de firma con llave ausente y rechazo de firma por política sin solicitud de desbloqueo.
- Reenvío de eliminación de llaves al agente aislado y pausa tras un desbloqueo fallido.
- Directorio privado, permisos del socket, bloqueo exclusivo, recuperación de socket propio obsoleto, rechazo de reemplazo de socket activo o archivo ajeno y detección de alias por symlink.
- Prueba con **OpenSSH `ssh-agent` y `ssh-add` reales**, aislados del agente del usuario. `ssh-add -L` recibe la llave cargada por el desbloqueador simulado y las firmas del agente real se verifican.
- Fuzzing de identidades durante cinco segundos: 200 447 ejecuciones, sin fallos.
- Plantilla LaunchAgent comprobada con `plutil -lint`.
- CLI: ayuda, rutas, argumentos inválidos, detección de endpoints recursivos y precedencia entre configuración guardada, agente de la sesión y opciones explícitas.
- Instalación en directorios temporales con gestores simulados para los tres formatos: copia de binario, configuración, registro, respaldo SSH, reinstalación sin duplicados y actualización entre versiones.
- Desinstalación conservando cambios externos al bloque SSH y el respaldo original; limpieza del archivo SSH creado por el instalador.
- Reversión tras fallar la activación y recuperación de los archivos y servicio anteriores; reanudación de una desinstalación interrumpida.
- Rechazo de servicios o archivos ajenos, cambios manuales en archivos administrados, cambios posteriores a una previsualización e instalaciones concurrentes.
- `--no-ssh` conserva incluso una configuración SSH mediante symlink.
- `install --dry-run` ejecutado con las rutas reales del usuario: no creó la instalación y el LaunchAgent generado pasó `plutil -lint`.

## Compilado

Binarios sin CGO para macOS, Linux y Windows, en amd64 y arm64, disponibles en `dist/`. Binario local en `bin/keepassxc-agent`.

Se comprobó también la compilación de los paquetes de pruebas para Windows amd64 con `go test -exec=true ./...`; esto **no ejecuta las pruebas Windows**.

## Pendiente de comprobar en sistemas reales

- Diálogo de desbloqueo y carga automática con una instalación real de KeePassXC y una bóveda del usuario.
- Ejecución nativa de las pruebas en Linux y Windows. El workflow preparado en `.github/workflows/test.yml` las ejecutará cuando el proyecto se publique en GitHub; no se ha ejecutado desde esta entrega.
- Integración de named pipes con KeePassXC/OpenSSH reales en Windows.
- Registro y ejecución reales de LaunchAgent, unidad systemd y tarea programada Windows. Las pruebas del instalador utilizan gestores simulados y no demuestran la activación real de esos servicios.
- Métodos de desbloqueo rápido concretos, como Touch ID o Windows Hello.

No se ha abierto la bóveda personal, cambiado la configuración SSH, instalado servicios ni modificado el agente SSH personal.

## Controlador de bandeja Windows — 17 de noviembre de 2026

- Implementado con Win32 y sin dependencias nuevas: menú nativo, iconos por conectividad, comprobación asíncrona, recuperación tras reiniciar Explorer y exclusión de instancias por usuario/sesión.
- Proxy y bandeja tienen tareas programadas independientes; abrir manualmente la bandeja usa su tarea registrada para permitir actualizar y desinstalar sin dejar el ejecutable ocupado.
- Comprobados localmente `go test -race ./...`, compilación Windows amd64 y `go vet` para Windows. Inspeccionados los iconos generados a partir del código.
- Añadidas pruebas exclusivas de Windows para la ejecución sin consola y propagación del código de salida; registro simulado de las dos tareas y protección de una tarea de bandeja ajena; tamaños ABI de Win32 y aceptación de los recursos de icono. Su compilación se verifica desde macOS; su ejecución corresponde al job Windows de CI.
- Pendiente la comprobación nativa del menú, las acciones de iniciar/detener/reiniciar, la salida independiente, el reinicio de Explorer, el inicio de sesión y la actualización/desinstalación con tareas reales. No se ejecutó la interfaz Windows desde macOS.

## Ciclo de vida — 17 de noviembre de 2026

- Recuperación de procesos Windows mediante el PID real del servidor del named pipe; comprobación del SID, ruta y hash del ejecutable frente al registro de instalación. La bandeja se identifica por su ventana y se verifica con las mismas restricciones.
- Cierre cooperativo mediante un evento privado del usuario, espera de terminación y recuperación de binarios antiguos mediante el handle verificado del proceso. Las tareas se deshabilitan durante la parada para evitar relanzamientos.
- Controles `start`, `stop` y `restart` de Windows compartidos por CLI y bandeja, serializados con instalación/desinstalación. El inicio evita duplicados y verifica el dueño del pipe; la parada conserva la configuración de inicio de sesión.
- La reinstalación detiene el endpoint de la configuración anterior aunque se cambie `--listen`; la reversión restablece la referencia al binario anterior.
- Pruebas locales para espera de liberación real de sockets, errores de sondeo, cancelación y recuperación del binario anterior. Pruebas Windows añadidas para cierre cooperativo, recuperación de una instancia antigua y rechazo de un servidor ajeno sin terminarlo; compiladas desde macOS, pendientes de ejecución nativa en CI/Windows.

## Ciclo de vida macOS y Linux — 17 de noviembre de 2026

- `start`, `stop` y `restart` implementados en las tres plataformas. Los controles Unix verifican credenciales del socket, usuario, identidad de creación del proceso y archivo ejecutable registrado antes de enviar señales. Linux usa `pidfd` para señalar la instancia identificada; macOS consulta credenciales, fecha de creación y el vnode del programa mediante `lsof` del sistema.
- Pruebas nativas en macOS arm64 con procesos aislados: cierre ordenado, cierre forzado de un proceso que ignora SIGTERM, rechazo de procesos ajenos y servicios editados, inicio sin duplicados, conservación del inicio automático y cambio de socket durante actualización.
- Prueba nativa con LaunchAgent temporal real: arranque, detección de la instancia existente, parada, arranque posterior y retirada. La etiqueta fue exclusiva de prueba y se retiró al finalizar; no se persistieron overrides de habilitación ni se tocaron servicios personales.
- Las mismas pruebas de procesos pasaron en Linux arm64 dentro de la VM de Podman.
- Prueba nativa de systemd de usuario en Linux arm64: arranque, inicio idempotente, parada conservando habilitación, segundo stop, nuevo inicio, reinicio con PID distinto y parada para retirada. Se utilizó una cuenta temporal dentro de la VM y el script la eliminó al terminar; la VM se apagó después.
- `go test -race ./...` y `go vet ./...` pasan localmente. Las pruebas con gestores reales son opt-in (`KAM_TEST_LAUNCHD=1` y `KAM_TEST_SYSTEMD=1`), para que la suite ordinaria no registre trabajos en una sesión personal.
- Queda pendiente comprobar el desbloqueo con KeePassXC real y sus métodos de autenticación. Estas pruebas no abrieron la bóveda ni usaron el agente SSH personal.

## Icono macOS y KDE — 17 de noviembre de 2026

- macOS: helper Swift/AppKit con NSStatusItem y menú nativo; controlador Go independiente del proxy. Compilado para arm64 y amd64 con destino macOS 12. El paquete de distribución incluye ambos ejecutables.
- KDE: StatusNotifierItem y menú D-Bus implementados en Go, sin GTK/Qt. Unidad systemd independiente y autoarranque de sesión limitado a KDE.
- Instalación, actualización conservando el helper verificado, opción `--no-tray`, permisos, retirada y reanudación tras eliminación del helper cubiertos por pruebas del instalador.
- El puente JSON de macOS se ejercita con un helper de prueba: detener, iniciar, salir, cierre del helper por EOF y retirada del socket. Salir conserva el estado del proxy simulado y no dispara otra acción de control.
- Protocolo KDE probado en Linux arm64 dentro de la VM con un bus D-Bus real y un watcher simulado: registro, firmas de propiedades e iconos, menú y habilitación de acciones, exclusión de instancias, recuperación del watcher, parada y salida independiente. Cuenta temporal eliminada y VM apagada al terminar. La prueba es opt-in con `KAM_TEST_DBUS=1`; CI la ejecuta en `dbus-run-session`.
- `go test -race ./...` y `go vet` para macOS, Linux y Windows; compilación de las pruebas Windows desde macOS (no ejecución nativa Windows).
- Inspección visual pendiente: se lanzó una vista previa aislada en macOS, pero el inspector de escritorio no respondió; no se obtuvo una captura ni se verificó visualmente su menú. No se dispone de una sesión Plasma con panel para comprobar la presentación en KDE. Las pruebas de protocolo no sustituyen esas comprobaciones.
- Sigue pendiente el desbloqueo con KeePassXC real. No se abrió la bóveda personal ni se instaló este controlador en la sesión del usuario.
