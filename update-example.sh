#/bin/sh

set -ux
# we don't use -e (fail on error here, because we need to handle explicit exit codes)

function suss {  
    echo "SUSS: $1"
    curl --fail "http://localhost:9993/$1"

    if [[ "$?" != "0" ]]; then
        echo "SUSS call failed, stop script execution"
        exit 1
    fi 
}

# check for updates first 
# https://dnf.readthedocs.io/en/latest/command_ref.html#check-update-command
dnf check-update
rc=$?
if [[ "$rc" != "100" ]]; then
    echo "No package updates available, exiting"
    exit 0
fi
if [[ "$rc" != "0" ]]; then
    echo "dnf check-update exited with code $rc, exiting"
    exit 1
fi

# synchronize with other hosts
suss synchronize

# teardown critical workload
suss teardown

# and finally run update
# https://dnf.readthedocs.io/en/latest/command_ref.html#upgrade-command-label
dnf update -y
rc=$?
if [[ "$rc" != "0" ]]; then
    echo "dnf update failed, releasing lock"
    suss release
    exit 1
fi

# check if reboot is requred
# https://dnf-plugins-core.readthedocs.io/en/latest/needs_restarting.html
dnf needs-restarting -r
rc=$?
if [[ "$rc" == "0" ]]; then
    echo "No reboot required, releasing lock"
    suss release
    exit 0
fi

# need reboot, release delayed
suss releasedelayed

# and finally reboot, use -t so that script has exit code 0
shutdown -t 1 -r
