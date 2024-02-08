#/bin/sh

set -u
# we don't use -e (fail on error here, because we need to handle explicit exit codes)

# alias yum for CENTOS-7
# command -v dnf > /dev/null || alias dnf="yum"

SUSS_URL=http://localhost:9993

function suss {  

    local max_attempts=5
    local attempt=0
    local rc=0

    while [[ $attempt < $max_attempts ]]; do
        echo "SUSS: $1"
        curl --fail --silent "$SUSS_URL/$1"
        rc=$?

        if [[ $rc == 0 ]]; then
            break
        fi

        attempt=$(( attempt + 1 ))
        sleep 10
        echo "SUSS call failed, retrying..."
    done

    if [[ $rc != "0" ]]; then
        echo "SUSS call failed $max_attempts times, stop script execution"
        exit 1
    fi 

}

# check for updates first 
# https://dnf.readthedocs.io/en/latest/command_ref.html#check-update-command
yum check-update
rc=$?
if [[ "$rc" == "0" ]]; then
    echo "No package updates available, exiting"
    exit 0
fi
if [[ "$rc" != "100" ]]; then
    echo "dnf check-update exited with code $rc, exiting"
    exit 1
fi

# start logstream, will be killed by trap
trap 'echo "stopping logstream";kill $log_pid' SIGINT SIGTERM EXIT
curl $SUSS_URL/logstream & log_pid=$!

# synchronize with other hosts
suss synchronize

# teardown critical workload
suss teardown

# and finally run update
# https://dnf.readthedocs.io/en/latest/command_ref.html#upgrade-command-label
yum update -y
rc=$?
if [[ "$rc" != "0" ]]; then
    echo "dnf update failed, releasing lock"
    suss release
    exit 1
fi

# check if reboot is requred
# https://dnf-plugins-core.readthedocs.io/en/latest/needs_restarting.html
needs-restarting -r
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
