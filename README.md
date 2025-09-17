# SSH Restarter

## **NAME**

**ssh\_restarter** - a command wrapper for on-demand, SSH-gated services.

-----

## **SYNOPSIS**

`ssh_restarter` \[**OPTIONS**] *command*

-----

## **DESCRIPTION**

**ssh\_restarter** is a utility that wraps an arbitrary command-line application, managing its lifecycle based on SSH client connections. It starts a dedicated SSH server that acts as a gatekeeper for the wrapped application.

The specified *command* is executed only when the first authorized user connects to the SSH server. The command is terminated after the last user disconnects, following a configurable idle timeout. This mechanism is designed to conserve system resources, such as RAM, by ensuring the application runs only when actively in use.

The primary function of the SSH server is to provide secure access to the application via port forwarding. Authorized users can establish a secure tunnel to the application's port without exposing it directly to the network. Users may also attach to the running application's terminal for interactive access.

-----

## **OPTIONS**

  * `-ssh_port <port>`
    Specifies the TCP port on which the **ssh\_restarter** SSH server will listen. The default is `22`.

  * `-fwd_port <port>`
    Specifies the destination TCP port on the local host to which all authenticated client connections are forwarded. The default is `25565`.

  * `-hostkey <path>`
    Specifies the path to the SSH private host key file. The default is `/etc/ssh/ssh_host_rsa`.

  * `-pwd <bcrypt_hash>`
    Provides the bcrypt hash of the password required for SSH authentication. This option is mandatory. If this flag is not provided, the value will be read from the `SSH_RESTARTER_PWD` environment variable. A hash can be generated using a standard utility, such as the tool available at `https://bcrypt-generator.com/`.

  * `-timeout <seconds>`
    Specifies the idle timeout in seconds. If no clients are connected for this duration, the wrapped command will be terminated. The default is `600` (10 minutes).

-----

## **EXAMPLE: MANAGING A MINECRAFT SERVER**

A common use case is the secure and resource-efficient management of a Minecraft server. A continuously running server consumes system resources and may be exposed to network threats. **ssh\_restarter** mitigates these issues by launching the server on-demand and protecting it behind an SSH login.

### **Server-Side Configuration**

To launch a Minecraft server that listens on port `25565`, managed by **ssh\_restarter** listening on port `2222` with a 5-minute timeout:

```bash
# Set the password hash in an environment variable
export SSH_RESTARTER_PWD='$2a$12$...'

# Launch ssh_restarter, which will wait for a connection
# before executing the java command.
./ssh_restarter \
    -ssh_port 2222 \
    -fwd_port 25565 \
    -timeout 300 \
    java -Xmx4G -Xms1G -jar server.jar nogui
```

### **Client-Side Connection**

A user must first establish an SSH tunnel to the server before connecting with the Minecraft client. This action will trigger **ssh\_restarter** to start the Minecraft server process.

1.  **Establish the SSH tunnel.**
    Execute the following command in a local terminal. The user will be prompted for the password corresponding to the configured bcrypt hash.

    ```bash
    ssh -L 25565:localhost:25565 -N user@<server_hostname_or_ip> -p 2222
    ```

      * The `-L 25565:localhost:25565` flag forwards the local port `25565` to port `25565` on the remote server through the secure channel.
      * The `-N` flag indicates that no remote command is to be executed; the connection is for port forwarding only.

2.  **Connect from Minecraft.**
    Launch the Minecraft client, navigate to the "Multiplayer" screen, and add a server with the address `localhost`. The game traffic will be transparently and securely tunneled to the remote server.