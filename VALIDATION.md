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
