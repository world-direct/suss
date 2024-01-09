#/bin/sh

set -ux

function suss {  
    echo "SUSS: $1"
    curl --fail "http://localhost:9993/$1"

    if [[ "$?" != "0" ]]; then
        echo "SUSS call failed, stop script execution"
        exit 1
    fi 
}

# check for updates first
yum check-update
if [[ "$?" != "100" ]]; then
    echo "No package updates available, exiting"
    exit 0
fi

# synchronize with other hosts
suss synchronize

# teardown critical workload
suss teardown

# and finally run update
yum update -y

# check if reboot is requred
needs-restarting -r
if [[ "$?" == "0" ]]; then
    echo "No reboot required, releasing lock"
    suss release
    exit 0
fi

# need reboot, release delayed
suss releasedelayed

# and finally reboot
shutdown -t 1 -r
