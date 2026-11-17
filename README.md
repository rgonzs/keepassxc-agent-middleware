# KeePassXC SSH agent middleware

Proxy SSH escrito en Go para macOS, Linux y Windows. Cuando SSH necesita una llave que KeePassXC retiró al bloquear la bóveda, solicita el desbloqueo en la interfaz de KeePassXC, espera la carga de la llave y continúa la petición pendiente.

Se ejecuta **como proceso de usuario en segundo plano**. En Windows incluye un controlador independiente en la bandeja del sistema. SSH solo sabe conectarse a un agente; no inicia ejecutables automáticamente. Por eso el proxy debe estar escuchando antes de usar SSH. El trabajo de desbloqueo sí ocurre a demanda. La activación por socket sería otra opción en Unix, pero no ofrece el mismo mecanismo en Windows y no está implementada.

```text
ssh / scp / sftp / git
          │ IdentityAgent
          ▼
  socket / named pipe del proxy
          ├── protocolo SSH ──► agente SSH original
          └── protocolo Browser ──► KeePassXC: solicitar desbloqueo
                                           │
                                      tú autenticas
                                           │
                              carga llaves al agente original
```

## Compilar

Instala la versión de Go indicada en `go.mod` o una posterior. No requiere Rust, CGO ni dependencias gráficas.

```sh
go build -trimpath -o bin/keepassxc-agent ./cmd/keepassxc-agent
```

En Windows, primero compila con consola para configurar y diagnosticar:

```powershell
go build -trimpath -o bin/keepassxc-agent.exe ./cmd/keepassxc-agent
```

El instalador ejecuta el proceso de servicio sin consola en Windows y conserva su supervisión mediante la tarea programada. Ejecutar `run` manualmente sí mantiene el proceso en la terminal. Si prefieres distribuir además un binario sin consola, puedes compilar una segunda copia:

```powershell
go build -trimpath -ldflags="-H=windowsgui" -o bin/keepassxc-agent-background.exe ./cmd/keepassxc-agent
```

## Descargar binarios de GitHub Actions

El workflow **Test and build** ejecuta las pruebas en macOS, Linux y Windows. Si pasan los tres sistemas, compila y publica seis artifacts: uno por sistema (`darwin`, `linux`, `windows`) y arquitectura (`amd64`, `arm64`).

En GitHub abre **Actions → Test and build → una ejecución completada → Artifacts** y descarga `keepassxc-agent-SISTEMA-ARQUITECTURA`. Cada artifact contiene un paquete con el ejecutable y este README: `.tar.gz` para macOS/Linux (conserva el permiso de ejecución) y `.zip` para Windows. Después de extraerlo puedes ejecutar `keepassxc-agent install` o `keepassxc-agent.exe install` desde esa carpeta.

Los artifacts se conservan durante 30 días. Se generan en pushes, pull requests y ejecuciones manuales con **Run workflow**. Son descargas de una ejecución de Actions; este workflow no crea una GitHub Release.

## Preparar KeePassXC

1. Mantén KeePassXC abierto en tu sesión gráfica, con la bóveda cargada aunque esté bloqueada.
2. Habilita **Ajustes → Integración con el navegador → Habilitar integración**. El proxy usa ese canal local; no necesita instalar una extensión del navegador.
3. Habilita **Ajustes → Agente SSH → Habilitar integración**.
4. En la entrada de tu llave, activa **Añadir llave al agente al abrir/desbloquear la base de datos** y **Retirar llave al bloquear/cerrar la base de datos**.
5. KeePassXC debe seguir apuntando al **agente original**, nunca al socket del proxy.

El proxy utiliza un intercambio de claves efímero y `get-databasehash` con `triggerUnlock`. Esa operación de KeePassXC no requiere asociar el cliente ni da acceso a las entradas de la bóveda. No guarda credenciales de asociación, contraseña maestra ni llaves SSH. Touch ID, Windows Hello u otro desbloqueo rápido dependen de la configuración y soporte de tu KeePassXC; si no están disponibles, autenticas con el método habitual.

## macOS y Linux: prueba manual

Desde la carpeta del proyecto, mantén este proceso en una terminal:

```sh
./bin/keepassxc-agent paths
./bin/keepassxc-agent run --listen "$HOME/.keepassxc-agent/agent.sock"
```

`--upstream` toma por defecto el `SSH_AUTH_SOCK` actual. Si no está definido, pasa la ruta del agente original explícitamente. El directorio del socket se crea con permisos `0700`; si ya existe debe pertenecer a tu usuario y tener esos permisos.

En otra terminal, configura únicamente los hosts deseados en `~/.ssh/config`:

```sshconfig
Host mi-servidor
    HostName servidor.example.com
    User usuario
    IdentityAgent ~/.keepassxc-agent/agent.sock
```

Para todos los hosts puedes usar `Host *`. Coloca las opciones generales después de las específicas, ya que OpenSSH usa el primer valor aplicable.

Bloquea KeePassXC y ejecuta `ssh mi-servidor`. Debe aparecer el diálogo de desbloqueo y la autenticación SSH continuará después de desbloquear. También puedes probar solo el listado, sin conectarte a un servidor:

```sh
SSH_AUTH_SOCK="$HOME/.keepassxc-agent/agent.sock" ssh-add -l
```

La asignación anterior afecta solo a ese comando. **No cambies globalmente `SSH_AUTH_SOCK` al proxy si KeePassXC va a heredarlo**: provocaría un bucle. Para Git basta con que utilice OpenSSH y lea el mismo archivo de configuración.

## Windows: OpenSSH nativo

Esta implementación utiliza named pipes. Necesitas el servicio **OpenSSH Authentication Agent** activo y KeePassXC configurado para **OpenSSH**, con la carga y retirada de llaves activadas. Pageant y el agente Unix de Git Bash no son los transportes de esta implementación.

```powershell
.\bin\keepassxc-agent.exe paths
.\bin\keepassxc-agent.exe run
```

El comando `paths` muestra el pipe del proxy, único para tu SID. Copia su nombre a `%USERPROFILE%\.ssh\config`, usando barras hacia delante para evitar escapes del archivo SSH:

```sshconfig
Host mi-servidor
    HostName servidor.example.com
    User usuario
    IdentityAgent //./pipe/keepassxc-agent-S-1-5-21-TU-SID
```

Sustituye `S-1-5-21-TU-SID` por el SID completo que aparezca en `paths`. Utiliza el cliente OpenSSH nativo de Windows; si Git usa otro `ssh.exe`, configúralo para utilizar ese cliente. KeePassXC seguirá conectado a `\\.\pipe\openssh-ssh-agent`.

El pipe del proxy tiene una ACL limitada al usuario actual. El pipe de Browser se detecta como `\\.\pipe\org.keepassxc.KeePassXC.BrowserServer_USERNAME`. `--browser` permite corregirlo si tu instalación usa otro nombre.

### Bandeja de Windows

`install` registra e inicia dos tareas del usuario: el proxy y su controlador de bandeja, ambas al iniciar sesión. El icono puede aparecer dentro de los iconos ocultos de Windows. El menú permite consultar el estado, actualizarlo, iniciar, detener o reiniciar el middleware y abrir `agent.log`. La conectividad se comprueba cada 15 segundos sin solicitar el desbloqueo; verde indica disponibilidad, ámbar una conexión pendiente o fallida y gris que el proxy no está accesible. El texto del menú y del tooltip también identifica el estado. La conectividad no demuestra que la bóveda esté desbloqueada.

**Salir de la bandeja** cierra solamente el controlador. **Detener middleware** detiene el proxy y mantiene la bandeja disponible para volver a iniciarlo; las conexiones SSH que dependan del proxy fallarán mientras esté detenido. Para abrir de nuevo el icono:

```powershell
.\keepassxc-agent.exe tray
```

La tarea de bandeja usa el nombre de la tarea del proxy con el sufijo `-Tray`. Las reinstalaciones actualizan las dos tareas y `uninstall` retira ambas. Los errores de inicio de la bandeja se guardan en `~/.keepassxc-agent/tray.log`. El controlador usa las API nativas de Windows, sin CGO ni bibliotecas gráficas externas.

## Estado y opciones

```sh
./bin/keepassxc-agent status --listen "$HOME/.keepassxc-agent/agent.sock"
./bin/keepassxc-agent help
```

`status` comprueba la conectividad del proxy y de KeePassXC y consulta las llaves del agente **original**. No solicita el desbloqueo. Un endpoint alcanzable no demuestra que una bóveda concreta esté desbloqueada; el comando lo identifica como conectividad. Devuelve código distinto de cero si algún endpoint está inaccesible. Después de `install`, utiliza automáticamente la configuración guardada. Puedes indicar `--listen`, `--upstream` y `--browser` para comprobar otros endpoints. También informa si el servicio está registrado; la conectividad del proxy es la comprobación de que está escuchando.

| Opción | Función |
| --- | --- |
| `--listen` | Endpoint propio del proxy; `paths` muestra el predeterminado. |
| `--upstream` | Agente original; por defecto `SSH_AUTH_SOCK` en Unix y el pipe OpenSSH en Windows. |
| `--browser` | Endpoint local de integración Browser de KeePassXC. |
| `--fingerprint SHA256:…` | Esperar una llave específica aunque haya otras en el agente. |
| `--timeout 60s` | Tiempo máximo para esperar el desbloqueo y la carga, incluida la espera por otra solicitud. |
| `--log-file RUTA` | Añadir logs a un archivo; por defecto usa stderr. El directorio debe existir. |
| `--verbose` | Logs de diagnóstico sin contenido de mensajes ni llaves. |

Si tienes otras llaves cargadas, utiliza `--fingerprint` con la huella obtenida de `ssh-add -l` mientras tu llave está cargada en el agente original. Sin ese filtro, un listado con alguna llave se considera suficiente. Una firma fallida para una llave ausente también dispara el desbloqueo; una firma rechazada para una llave que sigue cargada se devuelve sin mostrar un diálogo ajeno al motivo del rechazo.

## Instalación automática por usuario

El propio ejecutable es el instalador. No requiere otro paquete ni permisos de administrador. Prepara primero KeePassXC como se indica arriba.

En macOS/Linux:

```sh
./bin/keepassxc-agent install --dry-run
./bin/keepassxc-agent install
./bin/keepassxc-agent status
```

En Windows, desde una terminal normal de tu usuario:

```powershell
.\bin\keepassxc-agent.exe install --dry-run
.\bin\keepassxc-agent.exe install
.\bin\keepassxc-agent.exe status
```

`--dry-run` muestra las rutas, la definición del servicio y el bloque SSH sin escribir archivos ni ejecutar el gestor de servicios. `install` realiza lo siguiente:

1. Copia el ejecutable a `~/.keepassxc-agent/bin/` con un nombre basado en su contenido. Puedes mover o borrar el binario descargado después de instalar.
2. Guarda la configuración en `~/.keepassxc-agent/config.json`, el registro de archivos propios en `install.json` y los logs en `agent.log`.
3. Respalda tu archivo SSH existente en `~/.ssh/config.keepassxc-agent-backup-FECHA` y añade al final un bloque `Host *` con `IdentityAgent`. Las opciones específicas anteriores conservan la precedencia de OpenSSH.
4. Registra y arranca un **LaunchAgent** en macOS, un **servicio systemd de usuario** en Linux o una **tarea de tu usuario al iniciar sesión** en Windows.
5. Espera hasta que el endpoint del proxy sea alcanzable. Si falla la activación, intenta restaurar los archivos y la instalación anterior.

No es un servicio de sistema: necesita tu sesión gráfica y el KeePassXC de tu usuario. En Windows, la tarea usa un proceso oculto y no necesita una segunda compilación `windowsgui`. Cada gestor configura el reinicio tras fallos. En Linux necesitas systemd de usuario disponible; si no existe, la instalación devuelve un error y revierte los archivos.

El inicio automático queda registrado para futuras sesiones. Por ejemplo, en macOS la definición se coloca en `~/Library/LaunchAgents/org.keepassxc.agent-middleware.plist`; en Linux, en `~/.config/systemd/user/keepassxc-agent.service`. La tarea Windows incluye tu SID en el nombre para separar usuarios.

### Opciones del instalador

```sh
./bin/keepassxc-agent install --no-ssh
./bin/keepassxc-agent install --fingerprint 'SHA256:TU_HUELLA'
./bin/keepassxc-agent install --upstream /ruta/estable/del/agente
```

`--no-ssh` permite gestionar `IdentityAgent` manualmente y no modifica tu archivo SSH. Es la opción apropiada si tu archivo SSH es un symlink o pertenece a un sistema de dotfiles. Si ya había un bloque administrado por este instalador, la opción lo retira.

`--listen`, `--upstream`, `--browser`, `--fingerprint` y `--timeout` personalizan la instalación. Usa rutas absolutas para los sockets Unix. El socket instalado predeterminado es `~/.keepassxc-agent/agent.sock`; Windows usa el pipe privado del usuario.

Si indicas `--upstream` o `--browser` explícitamente, esa ruta queda fijada. En caso contrario el proceso busca los endpoints en su sesión actual; conserva la ruta original del agente como respaldo si el gestor no exporta `SSH_AUTH_SOCK`. En escritorios Linux con sockets variables, asegúrate de que el gestor de usuario recibe `SSH_AUTH_SOCK` al iniciar sesión, o usa una ruta de agente estable. Si el respaldo cambia, repite `install` desde una terminal con el agente correcto. El instalador no cambia globalmente `SSH_AUTH_SOCK` ni configura KeePassXC para usar el proxy.

`run`, `status` y `paths` leen automáticamente la configuración instalada, si existe. Las opciones explícitas de esos comandos tienen precedencia; `--config RUTA` permite elegir otro archivo para ellos.

### Actualizar o desinstalar

Para actualizar, ejecuta `install` desde el binario nuevo. Una reinstalación conserva las opciones guardadas, salvo las que indiques nuevamente, y no duplica el bloque SSH ni reemplaza el respaldo original. Las versiones anteriores del binario permanecen registradas para su retirada al desinstalar.

```sh
./bin/keepassxc-agent uninstall --dry-run
./bin/keepassxc-agent uninstall
```

También puedes ejecutar esos comandos desde el binario instalado que muestra `status`. La desinstalación detiene y retira el servicio, elimina los archivos administrados y retira únicamente su bloque SSH. **Conserva tus cambios fuera de ese bloque, los respaldos y los logs.** En Windows, si ejecutas la desinstalación desde el binario instalado, un ayudante lo elimina después de salir.

Los hashes del registro evitan reemplazar o eliminar archivos administrados que hayas editado manualmente. Si hay cambios, el instalador se detiene y muestra el archivo para que preserves o restaures tus ediciones. Para cambiar opciones del proxy utiliza de nuevo `install` con las opciones correspondientes. No sobrescribe servicios preexistentes que no pertenezcan a esta instalación. Una desinstalación interrumpida guarda progreso y puede continuarse ejecutando `uninstall` otra vez.

Las plantillas de `examples/` se mantienen como referencia para configuración manual; no son necesarias cuando usas `install`.

## Límites y comportamiento

- KeePassXC debe estar abierto con Browser habilitado; el proxy no lanza la aplicación ni escribe contraseñas por ti.
- El desbloqueo lo decide KeePassXC sobre la base activa; el proxy no selecciona una bóveda por nombre.
- Si borras las llaves manualmente mientras la base sigue desbloqueada, KeePassXC puede no volver a cargarlas. Bloquea y desbloquea la base, y verifica la opción de carga automática.
- Si cancelas el diálogo, se espera hasta el límite configurado sin volver a abrirlo en bucle. Hay una pausa de cinco segundos antes de otro intento fallido.
- Varias solicitudes comparten la coordinación del desbloqueo. Las llaves se consultan otra vez tras esperar; no se mantienen copias en caché.
- Se conserva una conexión al agente original por cada cliente para mantener el estado de extensiones como `session-bind`. No se reconecta ni se repite una firma cuando se pierde esa conexión. Las peticiones de modificación del agente se reenvían igual que al agente original.
- Límite de 64 conexiones, mensajes de hasta 1 MiB y espera de E/S del agente de cinco segundos. Las conexiones inactivas caducan tras cinco minutos.
- No habilites agent forwarding hacia servidores que no deban poder solicitar firmas o provocar diálogos locales.
- En Unix, un bloqueo exclusivo impide dos instancias en el mismo endpoint. Después de un cierre abrupto, solo recupera un socket propio que rechace conexiones; nunca elimina un socket activo, un symlink ni un archivo ajeno.
- Compatibilidad implementada con sockets Unix y named pipes OpenSSH. La compilación cruzada no equivale a validación de extremo a extremo en cada sistema. Consulta `VALIDATION.md` para la evidencia de esta entrega.

## Desarrollo y verificación

```sh
go test -race ./...
go vet ./...
go test ./internal/proxy -fuzz FuzzIdentities -fuzztime 5s
```

Las pruebas usan llaves efímeras y agentes aislados. En Unix también prueban con `ssh-agent` y `ssh-add` reales cuando están disponibles. CI ejecuta las pruebas en los tres sistemas. No se accede a tu bóveda ni se modifica tu agente personal.

Referencias de protocolo: [KeePassXC BrowserAction](https://github.com/keepassxreboot/keepassxc/blob/develop/src/browser/BrowserAction.cpp), [rutas locales de KeePassXC](https://github.com/keepassxreboot/keepassxc/blob/develop/src/browser/BrowserShared.cpp), [IdentityAgent de OpenSSH](https://man.openbsd.org/ssh_config#IdentityAgent).
