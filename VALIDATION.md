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

## Controlador de bandeja Windows — histórico, archivado

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

## CLI y PATH del usuario — 17 de noviembre de 2026

- Los controladores de Windows, macOS y KDE, sus pruebas y dependencias se movieron a `archive/tray/`, un módulo anidado excluido de la compilación y distribución. El servicio actual no registra una bandeja ni requiere Swift, AppKit o D-Bus para una interfaz.
- La actualización conserva compatibilidad para detener y retirar procesos y tareas de bandeja anteriores, verificando su pertenencia. Las pruebas simulan una instalación Unix con metadatos y archivos de bandeja y comprueban su retirada; la tarea Windows nueva registra únicamente el proxy. Los binarios viejos se conservan registrados para rollback y desinstalación.
- Lanzador estable en `~/.local/bin` para Unix y `.cmd` en `%LOCALAPPDATA%/Programs/keepassxc-agent` para Windows. No se reemplaza un ejecutable Windows en uso para actualizar el comando de la terminal.
- Pruebas del PATH: instalación, repetición, actualización, retirada con `--no-path`, desinstalación preservando cambios externos, permisos originales, rechazo de perfiles por symlink y del lanzador ajeno, detección de planes obsoletos y reversión tras fallo del servicio.
- El lanzador Unix se ejecuta realmente con argumentos que contienen espacios, comillas y metacaracteres; conserva argumentos y código de salida. Un shell real carga el bloque del PATH dos veces sin duplicar la entrada. Windows tiene una prueba nativa del `.cmd` desde PowerShell, compilada desde macOS y pendiente de ejecución en Windows/CI.
- Registro Windows: algoritmo de adición/retirada probado para entradas preexistentes, mayúsculas, variables de entorno, cambios posteriores y conservación del resto del PATH. La transacción se prueba con un almacén simulado, incluidos rechazo de cambios concurrentes y rollback. No se modificó el registro de una sesión Windows personal.
- Se mantiene la exclusión por endpoint: un segundo proxy sobre el mismo socket/pipe se rechaza; endpoints distintos admiten instancias independientes. `start`, `stop` y `restart` operan sobre la instalación predeterminada. No se añadió un bloqueo global por usuario.
- Sigue pendiente el desbloqueo con KeePassXC real. Estas pruebas no abren la bóveda, no cambian el PATH ni los perfiles personales y no instalan el servicio en la sesión del usuario.

- Suite final con detección de carreras aprobada en macOS; `go vet` para macOS/Linux/Windows y compilación de las pruebas Windows. Compilados seis ejecutables sin CGO (amd64/arm64 para cada sistema), con paquetes sin helper gráfico.
- Nueva ejecución nativa en Linux arm64: instalación/actualización/retirada del PATH y migración de bandeja con gestor simulado, lanzador y perfiles reales en carpetas temporales, sockets simultáneos independientes y ciclo completo con systemd de usuario real. Se eliminó la cuenta de prueba y se apagó la VM al finalizar.

## Consulta de logs — 17 de noviembre de 2026

- Nuevo comando `logs`, con `--lines`/`-n` (100 por defecto), `--follow`/`-f`, `--config` y `--log-file`.
- Lectura de la cola por bloques, sin cargar todo el histórico en memoria; seguimiento con reapertura del archivo para no mantener un handle que impida rotación en Windows.
- Pruebas de archivo vacío, línea parcial, líneas vacías, texto UTF-8, líneas mayores de 64 KiB, precedencia entre archivo explícito y configuración, argumentos inválidos y archivo inexistente.
- Pruebas con archivos temporales de adición, reemplazo, truncamiento, ausencia transitoria y seguimiento real cancelado por contexto. Ninguna prueba consulta los logs ni modifica los procesos personales.

## Listado de instancias — 17 de noviembre de 2026

- Nuevo comando `instances` (alias `instancias`) y salida `--json`: PID, endpoint, log, instancia predeterminada/adicional y metadatos del ejecutable/configuración.
- Cada ejecución de `run` registra su PID, identidad de creación del proceso y endpoint en un archivo privado escrito por renombrado atómico. El cierre ordenado retira únicamente ese archivo. Los registros de caídas se ignoran sin señalar ni terminar procesos.
- La consulta valida la identidad del proceso y el PID real del servidor del socket o pipe, con credenciales del usuario. Linux incorpora también el identificador de arranque para evitar coincidencias de PID/tiempo tras un reinicio. No se envían mensajes del protocolo SSH ni se consulta KeePassXC.
- Compatibilidad con la instancia predeterminada de una versión anterior mediante inspección del endpoint y comparación del ejecutable. Las instancias antiguas en endpoints personalizados necesitan reinicio para registrarse.
- Pruebas con dos endpoints simultáneos, registros inválidos y obsoletos, endpoint detenido, retirada del registro, descubrimiento de la instancia antigua, rechazo de otro ejecutable y caída forzada de un proceso hijo.
- Verificación de extremo a extremo en macOS con dos procesos del binario real, HOME/configuración/endpoints temporales: clasificación predeterminada/adicional, PID correcto, rechazo de duplicado, omisión tras SIGKILL, cierre ordenado y listado vacío final. Agente SSH y Browser apuntaron a rutas de prueba inexistentes; no se usaron servicios ni bóvedas personales.
- Las pruebas del paquete de instancias pasaron también de forma nativa en Linux arm64 con una cuenta temporal dentro de la VM. Cuenta retirada y VM apagada al terminar. Windows comprobado por compilación y `go vet`; ejecución nativa pendiente de Windows/CI.
