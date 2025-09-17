#!/bin/bash
# before running, you should have created two secrets:
# mcserver-hostkey: should be a bcrypt of the password
# mcserver-hostkey: should be a PEM private key (generate with openSSL)
podman build . -t mcserver
podman run --replace \
    --secret=mcserver-password,type=env,target=SSH_RESTARTER_PWD --secret=mcserver-hostkey,type=mount,target=ssh_host \
    -p 25565:22 -v /opt/minecraft:/mnt/minecraft \
    --name mcserver -dit mcserver \
    /bin/ssh_restarter --hostkey=/run/secrets/ssh_host /mnt/minecraft/start_server.sh
# run the below to install the systemd service
# podman generate systemd --new --name mcserver > ~/.config/systemd/user/mcserver.service