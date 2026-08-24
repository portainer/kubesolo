#!/bin/sh

set -e

# Function to handle errors
handle_error() {
    echo "❌ Error: $1"
    exit 1
}

# Function to detect init system
detect_init_system() {
    if command -v systemctl >/dev/null 2>&1 && [ -d /etc/systemd/system ]; then
        echo "systemd"
    elif [ -f /sbin/init ] && /sbin/init --version 2>/dev/null | grep -q upstart; then
        echo "upstart"
    elif command -v openrc >/dev/null 2>&1 || [ -f /sbin/openrc ]; then
        echo "openrc"
    elif [ -d /etc/s6 ] || command -v s6-svc >/dev/null 2>&1; then
        echo "s6"
    elif command -v runit >/dev/null 2>&1 || [ -d /etc/runit ]; then
        echo "runit"
    elif [ -d /etc/init.d ]; then
        echo "sysvinit"
    else
        echo "unknown"
    fi
}

# Function to detect if we're in a container or embedded environment
detect_environment() {
    if [ -f /.dockerenv ] || [ -f /run/.containerenv ]; then
        echo "container"
    elif [ -f /proc/device-tree/model ] && grep -qi "raspberry\|beagle\|odroid" /proc/device-tree/model 2>/dev/null; then
        echo "embedded"
    elif [ "$(uname -m)" = "armv7l" ] || [ "$(uname -m)" = "aarch64" ]; then
        echo "arm"
    else
        echo "standard"
    fi
}

# Function to check for Docker prerequisite
check_docker_prerequisite() {
    echo "🔍 Checking for Docker prerequisite conflicts..."

    # Check if Docker command exists
    if command -v docker >/dev/null 2>&1; then
        handle_error "Docker is installed on this system. Please remove Docker before installing KubeSolo as it can interfere with KubeSolo networking. See: https://docs.kubesolo.io/prerequisites"
    fi

    # Check if Docker daemon is running
    if [ -S /var/run/docker.sock ]; then
        handle_error "Docker daemon appears to be running (socket found at /var/run/docker.sock). Please stop and remove Docker before installing KubeSolo."
    fi

    # Check for Docker systemd service
    if command -v systemctl >/dev/null 2>&1; then
        if systemctl is-active --quiet docker 2>/dev/null; then
            handle_error "Docker service is active. Please stop and remove Docker before installing KubeSolo."
        fi
    fi

    echo "✅ No Docker installation detected"
}

# Function to check hostname RFC 1123 compliance
check_hostname_compliance() {
    echo "🔍 Checking hostname RFC 1123 compliance..."

    # Get the hostname
    CURRENT_HOSTNAME=$(hostname 2>/dev/null || echo "")

    if [ -z "$CURRENT_HOSTNAME" ]; then
        handle_error "Could not determine hostname. Please ensure hostname is properly configured."
    fi

    # Check if hostname contains uppercase letters
    if echo "$CURRENT_HOSTNAME" | grep -q '[A-Z]'; then
        handle_error "Hostname '$CURRENT_HOSTNAME' contains uppercase letters. RFC 1123 requires lowercase only. Please change hostname to lowercase."
    fi

    # Check RFC 1123 subdomain compliance using grep
    # Pattern: must start and end with alphanumeric, contain only lowercase alphanumeric, '-', or '.'
    if ! echo "$CURRENT_HOSTNAME" | grep -qE '^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$'; then
        handle_error "Hostname '$CURRENT_HOSTNAME' is not RFC 1123 compliant. Must contain only lowercase letters, numbers, hyphens, and dots, and must start and end with an alphanumeric character."
    fi

    # Additional check for length (RFC 1123 limit is 63 characters per label)
    if [ ${#CURRENT_HOSTNAME} -gt 253 ]; then
        handle_error "Hostname '$CURRENT_HOSTNAME' is too long (${#CURRENT_HOSTNAME} characters). Maximum allowed is 253 characters."
    fi

    echo "✅ Hostname '$CURRENT_HOSTNAME' is RFC 1123 compliant"
}

# Function to detect Alpine Linux
is_alpine() {
    [ -f /etc/alpine-release ]
}

# Function to check and optionally install nftables on Alpine.
# kube-proxy uses nftables mode on Alpine because iptables-legacy kernel modules
# are not available; the nft binary must be present for it to start.
check_nftables() {
    if ! is_alpine; then
        return
    fi

    echo "🔍 Checking nftables (required on Alpine for kube-proxy)..."

    if command -v nft >/dev/null 2>&1; then
        echo "✅ nftables (nft) is available"
        return
    fi

    if [ "$INSTALL_PREREQS" = "true" ]; then
        echo "📦 Installing nftables..."
        apk add --no-cache nftables || handle_error "Failed to install nftables. Please install it manually: apk add nftables"
        echo "✅ nftables installed"
    else
        handle_error "nftables (nft) is required on Alpine but was not found. Run with --install-prereqs to install automatically, or run: apk add nftables"
    fi
}

# Function to check iptables xt_comment module support
check_iptables_comment_module() {
    echo "🔍 Checking iptables xt_comment module support..."

    # Check if iptables command exists
    if ! command -v iptables >/dev/null 2>&1; then
        handle_error "iptables command not found. Please install iptables before proceeding."
    fi

    # Test iptables comment module by trying to create a test rule
    # Use a unique comment to avoid conflicts
    TEST_COMMENT="kubesolo-test-$$-$(date +%s)"

    # Try to add a test rule with comment (to a non-existent chain to avoid side effects)
    if ! iptables -t filter -A KUBESOLO_TEST_CHAIN -m comment --comment "$TEST_COMMENT" -j ACCEPT 2>/dev/null; then
        # Try to check if the module is available in a different way
        if [ -d /proc/sys/net ] && [ -r /proc/modules ]; then
            if ! grep -q "xt_comment" /proc/modules 2>/dev/null && ! modprobe xt_comment 2>/dev/null; then
                handle_error "iptables xt_comment module is not available. This module is required for KubeSolo networking. Please ensure your kernel has xt_comment support or install iptables-mod-extra package."
            fi
        else
            # Final fallback test - try to list rules with verbose output
            if ! iptables -t filter -L -v >/dev/null 2>&1; then
                handle_error "iptables does not appear to be working properly. Please check iptables installation and kernel module support."
            fi
            echo "⚠️  Could not verify xt_comment module support directly, but iptables appears functional"
        fi
    else
        # Clean up test rule if it was somehow added (shouldn't happen with non-existent chain)
        iptables -t filter -D KUBESOLO_TEST_CHAIN -m comment --comment "$TEST_COMMENT" -j ACCEPT 2>/dev/null || true
    fi

    echo "✅ iptables with xt_comment module support verified"
}

# On Alpine (OpenRC), the cgroups service must be enabled and running so that
# the cgroupv2 controllers are available before kubesolo starts.
# Without it /sys/fs/cgroup/cgroup.controllers is empty after a fresh install.
ensure_alpine_cgroups_service() {
    if ! is_alpine; then
        return
    fi

    # Only relevant on OpenRC systems
    if ! command -v rc-update >/dev/null 2>&1; then
        return
    fi

    # Check if controllers are already available (service may already be running)
    local available
    available=$(cat /sys/fs/cgroup/cgroup.controllers 2>/dev/null || echo "")
    if [ -n "$available" ]; then
        return
    fi

    echo "🔍 cgroups controllers not available — cgroups service needs to be enabled (Alpine/OpenRC)..."

    if [ "$INSTALL_PREREQS" = "true" ]; then
        echo "📦 Enabling cgroups service at boot..."
        rc-update add cgroups boot 2>/dev/null || true
        echo "📦 Starting cgroups service..."
        rc-service cgroups start || handle_error "Failed to start cgroups service. Please run: rc-update add cgroups boot && rc-service cgroups start"
        echo "✅ cgroups service enabled and started"
    else
        handle_error "cgroups controllers are not available. On Alpine, run: rc-update add cgroups boot && rc-service cgroups start — or re-run this script with --install-prereqs"
    fi
}

# Function to check for required cgroups controllers
check_cgroups() {
    echo "🔍 Checking for required cgroups controllers..."

    # Required controllers: cpuset, cpu, io, memory, pids
    local required_controllers="cpuset cpu io memory pids"
    local missing_controllers=""

    # Check if cgroups v2 is mounted (unified hierarchy)
    if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
        # cgroups v2 - check unified controllers
        local available_controllers
        available_controllers=$(cat /sys/fs/cgroup/cgroup.controllers 2>/dev/null || echo "")

        if [ -z "$available_controllers" ]; then
            handle_error "cgroups v2 is mounted but no controllers are available. Please ensure cgroups are properly configured."
        fi

        # Check each required controller
        for controller in $required_controllers; do
            if ! echo "$available_controllers" | grep -qw "$controller"; then
                missing_controllers="$missing_controllers $controller"
            fi
        done

        if [ -n "$missing_controllers" ]; then
            handle_error "Required cgroups v2 controllers are missing:$missing_controllers. Available controllers: $available_controllers. Please enable the required controllers in your kernel or boot parameters."
        fi

        echo "✅ All required cgroups v2 controllers are available: $available_controllers"
    elif [ -d /sys/fs/cgroup ]; then
        # cgroups v1 - check individual controller directories
        # Also handle hybrid mode (both v1 and v2)
        local found_controllers=""

        for controller in $required_controllers; do
            # Check if controller directory exists
            if [ -d "/sys/fs/cgroup/$controller" ]; then
                # Verify it's functional by checking for common files
                if [ -f "/sys/fs/cgroup/$controller/cgroup.procs" ] || [ -f "/sys/fs/cgroup/$controller/tasks" ] || [ -d "/sys/fs/cgroup/$controller/system.slice" ] 2>/dev/null; then
                    found_controllers="$found_controllers $controller"
                else
                    # Directory exists but might not be properly mounted
                    if mountpoint -q "/sys/fs/cgroup/$controller" 2>/dev/null || [ -f "/sys/fs/cgroup/$controller/cgroup.procs" ] 2>/dev/null; then
                        found_controllers="$found_controllers $controller"
                    else
                        missing_controllers="$missing_controllers $controller"
                    fi
                fi
            else
                missing_controllers="$missing_controllers $controller"
            fi
        done

        if [ -n "$missing_controllers" ]; then
            handle_error "Required cgroups v1 controllers are missing:$missing_controllers. Please ensure cgroups are mounted. You may need to add 'cgroup_enable=cpuset cgroup_enable=cpu cgroup_enable=io cgroup_memory=1 cgroup_enable=pids' to your kernel boot parameters."
        fi

        echo "✅ All required cgroups v1 controllers are available:$found_controllers"
    else
        handle_error "cgroups filesystem not found at /sys/fs/cgroup. Please ensure cgroups are enabled in your kernel and mounted. You may need to add 'cgroup_enable=cpuset cgroup_enable=cpu cgroup_enable=io cgroup_memory=1 cgroup_enable=pids' to your kernel boot parameters."
    fi
}

# Find PIDs of processes whose executable is the kubesolo binary.
# This matches on /proc/$pid/exe (the actual binary path) rather than the
# command-line string, so the install script itself is never matched even
# when its arguments contain the word "kubesolo" (e.g. --offline-install=/tmp/kubesolo).
find_kubesolo_binary_pids() {
    local kubesolo_binary="/usr/local/bin/kubesolo"
    local our_pid=$$
    local pids=""
    for piddir in /proc/[0-9]*/; do
        local pid
        pid=$(basename "$piddir")
        [ "$pid" = "$our_pid" ] && continue
        local exe
        exe=$(readlink "${piddir}exe" 2>/dev/null) || continue
        if [ "$exe" = "$kubesolo_binary" ]; then
            pids="$pids $pid"
        fi
    done
    echo "$pids"
}

# Function to stop running KubeSolo processes
stop_running_processes() {
    echo "🔍 Checking for running KubeSolo processes..."

    # Try to stop service first (graceful shutdown)
    if command -v systemctl >/dev/null 2>&1; then
        if systemctl is-active --quiet kubesolo 2>/dev/null; then
            echo "🛑 Stopping KubeSolo service (systemd)..."
            systemctl stop kubesolo 2>/dev/null || true
            sleep 2
        fi
    elif [ -f "/etc/init.d/kubesolo" ]; then
        if command -v service >/dev/null 2>&1; then
            if service kubesolo status >/dev/null 2>&1; then
                echo "🛑 Stopping KubeSolo service (init.d)..."
                service kubesolo stop 2>/dev/null || true
                sleep 2
            fi
        fi
    fi

    # Find remaining kubesolo processes by executable path, not cmdline string.
    # This avoids the script killing itself when invoked with a path that
    # contains "kubesolo" (e.g. --offline-install=/tmp/kubesolo).
    local pids
    pids=$(find_kubesolo_binary_pids)

    if [ -n "$pids" ]; then
        echo "🛑 Stopping remaining KubeSolo processes..."
        for pid in $pids; do
            if kill -0 "$pid" 2>/dev/null; then
                if [ -f "/proc/$pid/cmdline" ]; then
                    local cmdline
                    cmdline=$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || echo "unknown")
                    echo "   Stopping PID $pid: $cmdline"
                else
                    echo "   Stopping PID $pid"
                fi
                kill -TERM "$pid" 2>/dev/null || true
            fi
        done

        sleep 2

        # Force kill any that are still running
        pids=$(find_kubesolo_binary_pids)
        if [ -n "$pids" ]; then
            echo "🛑 Force stopping remaining processes..."
            for pid in $pids; do
                kill -KILL "$pid" 2>/dev/null || true
            done
            sleep 1
        fi

        local retries=5
        local wait_time=1
        while [ $retries -gt 0 ]; do
            pids=$(find_kubesolo_binary_pids)
            if [ -z "$pids" ]; then
                break
            fi
            echo "⏳ Waiting for processes to fully terminate (${retries} retries remaining)..."
            sleep $wait_time
            retries=$((retries - 1))
            wait_time=$((wait_time + 1))
        done

        # Final check
        pids=$(find_kubesolo_binary_pids)
        if [ -n "$pids" ]; then
            echo "⚠️  Some KubeSolo processes may still be running, but continuing..."
        else
            echo "✅ KubeSolo processes stopped"
        fi
    else
        echo "✅ No running KubeSolo processes found"
    fi
}

# Function to stop processes holding KubeSolo ports
stop_port_processes() {
    echo "🔍 Checking for processes holding KubeSolo ports..."

    # KubeSolo ports: 2379 (Kine), 6443 (API Server), 10443 (Webhook), 6060 (pprof)
    local ports="2379 6443 10443 6060"
    local found_kubesolo_processes=false
    local found_non_kubesolo_processes=false

    for port in $ports; do
        local pids=""
        local port_name=""

        case $port in
            2379) port_name="Kine (etcd replacement)" ;;
            6443) port_name="API Server" ;;
            10443) port_name="Webhook" ;;
            6060) port_name="pprof server" ;;
        esac

        # Try lsof first (more reliable)
        if command -v lsof >/dev/null 2>&1; then
            pids=$(lsof -ti ":$port" 2>/dev/null || true)
        # Fallback to ss
        elif command -v ss >/dev/null 2>&1; then
            pids=$(ss -lptn "sport = :$port" 2>/dev/null | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u || true)
        # Fallback to netstat
        elif command -v netstat >/dev/null 2>&1; then
            pids=$(netstat -tlnp 2>/dev/null | grep ":$port " | awk '{print $7}' | cut -d'/' -f1 | grep -E '^[0-9]+$' | sort -u || true)
        fi

        if [ -n "$pids" ]; then
            for pid in $pids; do
                # Check if it's actually a kubesolo process by executable path, not
                # cmdline string, to avoid false-positives when the install script
                # path contains "kubesolo" (e.g. --offline-install=/tmp/kubesolo).
                local is_kubesolo=false
                local exe
                exe=$(readlink "/proc/$pid/exe" 2>/dev/null || echo "")
                if [ "$exe" = "/usr/local/bin/kubesolo" ]; then
                    is_kubesolo=true
                fi

                # Only stop kubesolo-related processes
                if [ "$is_kubesolo" = "true" ]; then
                    if [ "$found_kubesolo_processes" = "false" ]; then
                        echo "🛑 Stopping KubeSolo processes holding ports..."
                        found_kubesolo_processes=true
                    fi
                    echo "   Stopping PID $pid on port $port ($port_name)"
                    kill -TERM "$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
                else
                    # Track non-kubesolo processes but don't list them individually
                    found_non_kubesolo_processes=true
                fi
            done
        fi
    done

    if [ "$found_kubesolo_processes" = "true" ]; then
        echo "⏳ Waiting for ports to be released..."
        sleep 2
        echo "✅ KubeSolo port processes stopped"
    else
        echo "✅ No KubeSolo processes found holding ports"
    fi

    if [ "$found_non_kubesolo_processes" = "true" ]; then
        echo "ℹ️  Some KubeSolo ports are in use by non-KubeSolo processes - continuing with the installation"
    fi
}

# Helper function to check if a PID is in our process tree (ancestor)
# Returns 0 (true) if PID is an ancestor, 1 (false) otherwise
# This walks up the actual process tree to verify ancestry
is_pid_in_process_tree() {
    local target_pid="$1"
    local current_pid=$$
    local parent_pid="${PPID:-}"

    # Validate target_pid is numeric
    if ! echo "$target_pid" | grep -qE '^[0-9]+$'; then
        return 1
    fi

    # Check if it's current or parent (direct relationship)
    if [ "$target_pid" = "$current_pid" ] || [ "$target_pid" = "$parent_pid" ]; then
        return 0
    fi

    local check_pid="$parent_pid"
    local depth=0
    local max_depth=15

    while [ -n "$check_pid" ] && [ "$check_pid" != "1" ] && [ "$check_pid" != "0" ] && [ "$depth" -lt "$max_depth" ]; do
        if [ "$check_pid" = "$target_pid" ]; then
            return 0
        fi

        if [ -f "/proc/$check_pid/status" ]; then
            local parent_from_status
            parent_from_status=$(grep "^PPid:" "/proc/$check_pid/status" 2>/dev/null | awk '{print $2}' || echo "")
            if [ -n "$parent_from_status" ] && echo "$parent_from_status" | grep -qE '^[0-9]+$'; then
                check_pid="$parent_from_status"
            else
                break
            fi
        else
            break
        fi
        depth=$((depth + 1))
    done

    return 1
}

# Helper function to check if we're running under kubesolo
# Only returns true if we're actually a direct child of a kubesolo process
should_skip_cleanup() {
    local current_pid=$$
    local parent_pid="${PPID:-}"

    if [ -n "$parent_pid" ] && [ -f "/proc/$parent_pid/exe" ]; then
        local parent_exe
        parent_exe=$(readlink -f "/proc/$parent_pid/exe" 2>/dev/null || echo "")
        if echo "$parent_exe" | grep -q "kubesolo"; then
            if [ -f "/proc/$parent_pid/cmdline" ]; then
                local parent_cmdline
                parent_cmdline=$(cat "/proc/$parent_pid/cmdline" 2>/dev/null | tr '\0' ' ' || echo "")
                if echo "$parent_cmdline" | grep -q "kubesolo"; then
                    return 0
                fi
            fi
        fi
    fi

    if [ -f "/proc/$current_pid/exe" ]; then
        local current_exe
        current_exe=$(readlink -f "/proc/$current_pid/exe" 2>/dev/null || echo "")
        if echo "$current_exe" | grep -q "kubesolo"; then
            return 0
        fi
    fi

    return 1
}

# Function to clean up file conflicts
cleanup_file_conflicts() {
    echo "🔍 Checking for file conflicts..."

    local install_path="/usr/local/bin/kubesolo"
    local config_path="$CONFIG_PATH"
    local cleanup_needed=false
    local current_pid=$$
    local parent_pid="${PPID:-}"

    # $$ and $PPID are constant for the lifetime of the script — compute once
    local running_under_kubesolo=false
    if should_skip_cleanup; then
        running_under_kubesolo=true
        echo "⚠️  Script appears to be running under kubesolo - being extra careful with cleanup"
    fi

    # Check if binary exists and is in use
    if [ -f "$install_path" ]; then
        if command -v lsof >/dev/null 2>&1; then
            local binary_pids
            binary_pids=$(lsof -t "$install_path" 2>/dev/null || true)
            if [ -n "$binary_pids" ]; then
                cleanup_needed=true
                echo "🛑 Stopping processes using binary $install_path..."

                local cleaned_any=false
                for pid in $binary_pids; do
                    # Never kill PID 0 (kernel) or PID 1 (init) — would take down the system
                    if [ "$pid" -le 1 ] 2>/dev/null; then
                        continue
                    fi

                    # Always skip if this PID is in our process tree (safety first)
                    if is_pid_in_process_tree "$pid"; then
                        if [ "$running_under_kubesolo" = "true" ]; then
                            echo "   Skipping PID $pid (in our process tree)"
                        fi
                        continue
                    fi

                    # Only kill if the executable is actually the kubesolo binary.
                    # Checking /proc/$pid/exe avoids false-positives from cmdline
                    # matching (e.g. lsof returning parent/init processes).
                    local exe
                    exe=$(readlink "/proc/$pid/exe" 2>/dev/null || echo "")
                    if [ "$exe" != "/usr/local/bin/kubesolo" ]; then
                        continue
                    fi

                    if kill -0 "$pid" 2>/dev/null; then
                        cleaned_any=true
                        echo "   Stopping PID $pid"
                        (kill -TERM "$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null) || true
                    fi
                done

                if [ "$cleaned_any" = "false" ] && [ "$running_under_kubesolo" = "true" ]; then
                    echo "   No safe processes to clean up (all are in our process tree)"
                fi
                if [ "$cleaned_any" = "true" ]; then
                    sleep 2
                    binary_pids=$(lsof -t "$install_path" 2>/dev/null || true)
                    local remaining_pids=""
                    for pid in $binary_pids; do
                        if [ "$pid" != "$current_pid" ] && [ "$pid" != "$parent_pid" ]; then
                            remaining_pids="$remaining_pids $pid"
                        fi
                    done
                    if [ -n "$remaining_pids" ]; then
                        echo "⚠️  Some processes may still be using the binary, but continuing..."
                    fi
                fi
            else
                echo "ℹ️  Binary $install_path exists (will be replaced during installation)"
            fi
        else
            echo "ℹ️  Binary $install_path exists (will be replaced during installation)"
        fi
    fi

    # Check for socket files that might be in use
    local socket_files="
        $config_path/containerd/containerd.sock
        $config_path/kine/socket
        /run/containerd/containerd.sock
    "

    for socket_file in $socket_files; do
        if [ -S "$socket_file" ]; then
            if command -v lsof >/dev/null 2>&1; then
                local socket_pids
                socket_pids=$(lsof -t "$socket_file" 2>/dev/null || true)
                if [ -n "$socket_pids" ]; then
                    cleanup_needed=true
                    echo "🛑 Stopping processes using socket $socket_file..."

                    local cleaned_any=false
                    for pid in $socket_pids; do
                        if is_pid_in_process_tree "$pid"; then
                            if [ "$running_under_kubesolo" = "true" ]; then
                                echo "   Skipping PID $pid (in our process tree)"
                            fi
                            continue
                        fi

                        if kill -0 "$pid" 2>/dev/null; then
                            local is_kubesolo=false
                            if [ -f "/proc/$pid/cmdline" ]; then
                                local cmdline
                                cmdline=$(cat "/proc/$pid/cmdline" 2>/dev/null | tr '\0' ' ' || echo "")
                                if echo "$cmdline" | grep -q "kubesolo"; then
                                    is_kubesolo=true
                                fi
                            fi

                            if [ "$is_kubesolo" = "true" ] || [ "$running_under_kubesolo" = "false" ]; then
                                cleaned_any=true
                                echo "   Stopping PID $pid"
                                (kill -TERM "$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null) || true
                            fi
                        fi
                    done

                    if [ "$cleaned_any" = "false" ] && [ "$running_under_kubesolo" = "true" ]; then
                        echo "   No safe processes to clean up (all are in our process tree)"
                    fi
                    if [ "$cleaned_any" = "true" ]; then
                        sleep 1
                    fi
                fi
            else
                echo "ℹ️  Socket file $socket_file exists (will be cleaned up)"
                rm -f "$socket_file" 2>/dev/null || true
            fi
        fi
    done

    # Clean up PID file
    local pidfile="/var/run/kubesolo.pid"
    if [ -f "$pidfile" ]; then
        local pid
        pid=$(cat "$pidfile" 2>/dev/null || echo "")
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            if is_pid_in_process_tree "$pid"; then
                echo "⚠️  PID file contains process in our process tree - skipping cleanup to avoid termination"
            else
                local is_kubesolo=false
                if [ -f "/proc/$pid/cmdline" ]; then
                    local cmdline
                    cmdline=$(cat "/proc/$pid/cmdline" 2>/dev/null | tr '\0' ' ' || echo "")
                    if echo "$cmdline" | grep -q "kubesolo"; then
                        is_kubesolo=true
                    fi
                fi

                if [ "$is_kubesolo" = "true" ] || [ "$running_under_kubesolo" = "false" ]; then
                    cleanup_needed=true
                    echo "🛑 Stopping process from PID file $pidfile (PID: $pid)..."
                    (kill -TERM "$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null) || true
                    sleep 1
                elif [ "$running_under_kubesolo" = "true" ]; then
                    echo "⚠️  PID file contains non-kubesolo process - skipping cleanup"
                fi
            fi
        fi
        if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null || ! is_pid_in_process_tree "$pid"; then
            rm -f "$pidfile" 2>/dev/null || true
            echo "ℹ️  Cleaned up PID file"
        fi
    fi

    if [ "$cleanup_needed" = "true" ]; then
        echo "⏳ Waiting for file handles to be released..."
        sleep 2
        echo "✅ File conflicts resolved"
    else
        echo "✅ No file conflicts detected"
    fi
}

# Function to generate proxy environment variables
generate_proxy_env() {
    if [ -n "$PROXY" ]; then
        echo "Environment=\"HTTP_PROXY=$PROXY\""
        echo "Environment=\"HTTPS_PROXY=$PROXY\""
        echo "Environment=\"NO_PROXY=localhost,127.0.0.1\""
    fi
}

# Function to generate proxy exports for shell scripts
generate_proxy_exports() {
    if [ -n "$PROXY" ]; then
        echo "export HTTP_PROXY=\"$PROXY\""
        echo "export HTTPS_PROXY=\"$PROXY\""
        echo "export NO_PROXY=\"localhost,127.0.0.1\""
    fi
}

# Function to generate proxy environment variables for upstart
generate_proxy_env_upstart() {
    if [ -n "$PROXY" ]; then
        echo "env HTTP_PROXY=\"$PROXY\""
        echo "env HTTPS_PROXY=\"$PROXY\""
        echo "env NO_PROXY=\"localhost,127.0.0.1\""
    fi
}

# Function to create systemd service
create_systemd_service() {
    SERVICE_PATH="/etc/systemd/system/$APP_NAME.service"
    cat <<EOF > "$SERVICE_PATH" || handle_error "Failed to create systemd service file"
[Unit]
Description=$APP_NAME Service
After=network.target

[Service]
ExecStart=$INSTALL_PATH $CMD_ARGS
Restart=always
RestartSec=3
OOMScoreAdjust=-500
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal
$(generate_proxy_env)

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reexec || handle_error "Failed to reexecute systemd daemon"
    systemctl daemon-reload || handle_error "Failed to reload systemd daemon"
    systemctl enable "$APP_NAME" || handle_error "Failed to enable $APP_NAME service"
    systemctl restart "$APP_NAME" || handle_error "Failed to start $APP_NAME service"
    echo "✅ $APP_NAME service created and started with systemd"
}

# Function to create SysV init script
create_sysvinit_service() {
    SERVICE_PATH="/etc/init.d/$APP_NAME"
    cat <<EOF > "$SERVICE_PATH" || handle_error "Failed to create SysV init script"
#!/bin/sh
### BEGIN INIT INFO
# Provides:          $APP_NAME
# Required-Start:    \$network \$local_fs
# Required-Stop:     \$network \$local_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: $APP_NAME service
# Description:       KubeSolo single-node Kubernetes distribution
### END INIT INFO

$(generate_proxy_exports)

DAEMON="$INSTALL_PATH"
DAEMON_ARGS="$CMD_ARGS"
PIDFILE="/var/run/$APP_NAME.pid"
USER="root"

. /lib/lsb/init-functions

case "\$1" in
    start)
        log_daemon_msg "Starting $APP_NAME"
        start-stop-daemon --start --quiet --pidfile \$PIDFILE --make-pidfile --background --chuid \$USER --exec \$DAEMON -- \$DAEMON_ARGS
        log_end_msg \$?
        ;;
    stop)
        log_daemon_msg "Stopping $APP_NAME"
        start-stop-daemon --stop --quiet --pidfile \$PIDFILE
        RETVAL=\$?
        [ \$RETVAL -eq 0 ] && rm -f \$PIDFILE
        log_end_msg \$RETVAL
        ;;
    restart)
        \$0 stop
        \$0 start
        ;;
    status)
        status_of_proc -p \$PIDFILE \$DAEMON "$APP_NAME" && exit 0 || exit \$?
        ;;
    *)
        echo "Usage: \$0 {start|stop|restart|status}"
        exit 1
        ;;
esac

exit 0
EOF

    chmod +x "$SERVICE_PATH" || handle_error "Failed to make SysV init script executable"

    # Enable service for different runlevels
    if command -v update-rc.d >/dev/null 2>&1; then
        update-rc.d "$APP_NAME" defaults || handle_error "Failed to enable $APP_NAME service"
    elif command -v chkconfig >/dev/null 2>&1; then
        chkconfig --add "$APP_NAME" || handle_error "Failed to add $APP_NAME service"
        chkconfig "$APP_NAME" on || handle_error "Failed to enable $APP_NAME service"
    fi

    # Start the service - try service command first, fall back to direct init script
    if command -v service >/dev/null 2>&1; then
        service "$APP_NAME" start || handle_error "Failed to start $APP_NAME service"
    else
        "/etc/init.d/$APP_NAME" start || handle_error "Failed to start $APP_NAME service"
    fi
    echo "✅ $APP_NAME service created and started with SysV init"
}

# Function to create OpenRC service
create_openrc_service() {
    SERVICE_PATH="/etc/init.d/$APP_NAME"
    cat <<EOF > "$SERVICE_PATH" || handle_error "Failed to create OpenRC service script"
#!/sbin/openrc-run

$(generate_proxy_exports)

name="$APP_NAME"
description="KubeSolo single-node Kubernetes distribution"
command="$INSTALL_PATH"
command_args="$CMD_ARGS"
command_background=true
pidfile="/var/run/\${RC_SVCNAME}.pid"
command_user="root"

depend() {
    need net
    after firewall
}
EOF

    chmod +x "$SERVICE_PATH" || handle_error "Failed to make OpenRC service script executable"
    rc-update add "$APP_NAME" default || handle_error "Failed to enable $APP_NAME service"
    rc-service "$APP_NAME" start || handle_error "Failed to start $APP_NAME service"
    echo "✅ $APP_NAME service created and started with OpenRC"
}

# Function to create s6 service
create_s6_service() {
    S6_SERVICE_DIR="/etc/s6/sv/$APP_NAME"
    mkdir -p "$S6_SERVICE_DIR" || handle_error "Failed to create s6 service directory"

    cat <<EOF > "$S6_SERVICE_DIR/run" || handle_error "Failed to create s6 run script"
#!/bin/sh
$(generate_proxy_exports)
exec $INSTALL_PATH $CMD_ARGS
EOF

    chmod +x "$S6_SERVICE_DIR/run" || handle_error "Failed to make s6 run script executable"

    # Create finish script for proper cleanup
    cat <<EOF > "$S6_SERVICE_DIR/finish"
#!/bin/sh
echo "$APP_NAME service finished"
EOF
    chmod +x "$S6_SERVICE_DIR/finish"

    # Enable and start service
    if [ -d /etc/s6/adminsv/default ]; then
        ln -sf "$S6_SERVICE_DIR" "/etc/s6/adminsv/default/$APP_NAME" 2>/dev/null || true
    fi

    if command -v s6-svc >/dev/null 2>&1; then
        s6-svc -u "$S6_SERVICE_DIR" || handle_error "Failed to start $APP_NAME with s6"
    fi
    echo "✅ $APP_NAME service created and started with s6"
}

# Function to create runit service
create_runit_service() {
    RUNIT_SERVICE_DIR="/etc/runit/sv/$APP_NAME"
    mkdir -p "$RUNIT_SERVICE_DIR" || handle_error "Failed to create runit service directory"

    cat <<EOF > "$RUNIT_SERVICE_DIR/run" || handle_error "Failed to create runit run script"
#!/bin/sh
$(generate_proxy_exports)
exec $INSTALL_PATH $CMD_ARGS
EOF

    chmod +x "$RUNIT_SERVICE_DIR/run" || handle_error "Failed to make runit run script executable"

    # Enable service
    if [ -d /var/service ]; then
        ln -sf "$RUNIT_SERVICE_DIR" "/var/service/$APP_NAME" || handle_error "Failed to enable $APP_NAME service"
    elif [ -d /etc/runit/runsvdir/default ]; then
        ln -sf "$RUNIT_SERVICE_DIR" "/etc/runit/runsvdir/default/$APP_NAME" || handle_error "Failed to enable $APP_NAME service"
    fi

    echo "✅ $APP_NAME service created and started with runit"
}

# Function to create upstart service
create_upstart_service() {
    SERVICE_PATH="/etc/init/$APP_NAME.conf"
    cat <<EOF > "$SERVICE_PATH" || handle_error "Failed to create upstart service file"
description "$APP_NAME service"
author "KubeSolo"

start on runlevel [2345]
stop on runlevel [!2345]

respawn
respawn limit 10 5

$(generate_proxy_env_upstart)

exec $INSTALL_PATH $CMD_ARGS
EOF

    initctl reload-configuration || handle_error "Failed to reload upstart configuration"
    initctl start "$APP_NAME" || handle_error "Failed to start $APP_NAME service"
    echo "✅ $APP_NAME service created and started with upstart"
}

# Function to run in foreground mode
run_foreground() {
    echo "🚀 Starting $APP_NAME in foreground mode..."
    echo "📝 Command: $INSTALL_PATH $CMD_ARGS"
    echo "⚠️  Press Ctrl+C to stop the service"
    echo "💡 To run in background, use: nohup $INSTALL_PATH $CMD_ARGS > /var/log/$APP_NAME.log 2>&1 &"

    # Set proxy environment variables if configured
    if [ -n "$PROXY" ]; then
        export HTTP_PROXY="$PROXY"
        export HTTPS_PROXY="$PROXY"
        export NO_PROXY="localhost,127.0.0.1"
    fi

    eval "exec \"$INSTALL_PATH\" $CMD_ARGS"
}

# Function to run as daemon
run_daemon() {
    PIDFILE="/var/run/$APP_NAME.pid"
    LOGFILE="/var/log/$APP_NAME.log"

    echo "🚀 Starting $APP_NAME as daemon..."

    # Create log directory if it doesn't exist
    mkdir -p "$(dirname "$LOGFILE")" || handle_error "Failed to create log directory"

    # Set proxy environment variables if configured
    if [ -n "$PROXY" ]; then
        export HTTP_PROXY="$PROXY"
        export HTTPS_PROXY="$PROXY"
        export NO_PROXY="localhost,127.0.0.1"
    fi

    # Start daemon - use eval to properly handle arguments
    eval "nohup \"$INSTALL_PATH\" $CMD_ARGS > \"$LOGFILE\" 2>&1 &"
    echo $! > "$PIDFILE" || handle_error "Failed to write PID file"

    echo "✅ $APP_NAME started as daemon (PID: $(cat "$PIDFILE"))"
    echo "📋 Logs: tail -f $LOGFILE"
    echo "🛑 Stop: kill \$(cat $PIDFILE)"
}

# Function to download the binary archive and install script for offline use.
# Performs its own minimal arch/libc detection — no root or install-path globals needed.
download_bundle() {
    local outdir="$1"
    local os arch libc_suffix archive bin_url script_url

    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)
    case "$arch" in
        x86_64)  arch="amd64" ;;
        aarch64) arch="arm64" ;;
        armv7l)  arch="arm"   ;;
        riscv64) arch="riscv64" ;;
        *) handle_error "Unsupported architecture: $arch" ;;
    esac

    libc_suffix=""
    if [ -f /lib/ld-musl-*.so.1 ] || [ -f /usr/lib/ld-musl-*.so.1 ]; then
        if [ "$arch" = "amd64" ] || [ "$arch" = "arm64" ]; then
            libc_suffix="-musl"
        else
            handle_error "musl libc detected but musl builds are only available for amd64 and arm64. Current: $arch"
        fi
    fi

    local offline_suffix=""
    if [ "$OFFLINE" = "true" ]; then
        offline_suffix="-offline"
    fi

    archive="kubesolo-${KUBESOLO_VERSION}-${os}-${arch}${libc_suffix}${offline_suffix}.tar.gz"
    bin_url="https://github.com/portainer/kubesolo/releases/download/${KUBESOLO_VERSION}/${archive}"
    script_url="https://get.kubesolo.io"

    mkdir -p "$outdir" || handle_error "Failed to create output directory: $outdir"

    echo "📥 Downloading kubesolo ${KUBESOLO_VERSION} (${os}/${arch}${libc_suffix})..."
    curl -sfL "$bin_url" -o "$outdir/$archive" || handle_error "Failed to download from $bin_url"
    echo "✅ Binary archive saved to: $outdir/$archive"

    echo "📥 Downloading install script..."
    curl -sfL "$script_url" -o "$outdir/install.sh" || handle_error "Failed to download install script"
    chmod +x "$outdir/install.sh"
    echo "✅ Install script saved to: $outdir/install.sh"

    echo ""
    echo "✅ Download complete! Files saved to: $outdir"
    echo "💡 To install offline, transfer these files to the target machine and run:"
    echo "   sudo sh install.sh --offline-install=$archive"
}

# Function to install the kubesolo binary from a local archive/binary or by downloading it.
# Requires globals: KUBESOLO_OFFLINE_INSTALL, BIN_URL, APP_NAME, INSTALL_PATH, KUBESOLO_VERSION
install_binary() {
    local temp_dir

    if [ -n "$KUBESOLO_OFFLINE_INSTALL" ]; then
        [ -e "$KUBESOLO_OFFLINE_INSTALL" ] || handle_error "Specified offline-install path does not exist: $KUBESOLO_OFFLINE_INSTALL"
        temp_dir=$(mktemp -d -p "$HOME") || handle_error "Failed to create temporary directory"

        case "$KUBESOLO_OFFLINE_INSTALL" in
            *.tar.gz|*.tgz)
                echo "📦 Extracting $APP_NAME from local archive $KUBESOLO_OFFLINE_INSTALL..."
                tar -xzf "$KUBESOLO_OFFLINE_INSTALL" -C "$temp_dir" || handle_error "Failed to extract $KUBESOLO_OFFLINE_INSTALL"
                echo "📝 Installing binary..."
                mv "$temp_dir/kubesolo" "$INSTALL_PATH" || handle_error "Failed to move binary to $INSTALL_PATH"
                ;;
            *.zip)
                command -v unzip >/dev/null 2>&1 || handle_error "unzip is required to extract .zip archives but was not found"
                echo "📦 Extracting $APP_NAME from local zip archive $KUBESOLO_OFFLINE_INSTALL..."
                unzip -o "$KUBESOLO_OFFLINE_INSTALL" -d "$temp_dir" || handle_error "Failed to extract $KUBESOLO_OFFLINE_INSTALL"
                echo "📝 Installing binary..."
                mv "$temp_dir/kubesolo" "$INSTALL_PATH" || handle_error "Failed to move binary to $INSTALL_PATH"
                ;;
            *)
                echo "📝 Installing binary from local path $KUBESOLO_OFFLINE_INSTALL..."
                cp "$KUBESOLO_OFFLINE_INSTALL" "$temp_dir/kubesolo" || handle_error "Failed to copy binary to temporary directory"
                mv "$temp_dir/kubesolo" "$INSTALL_PATH" || handle_error "Failed to move binary to $INSTALL_PATH"
                ;;
        esac

        rm -rf "$temp_dir"
    else
        temp_dir=$(mktemp -d -p "$HOME") || handle_error "Failed to create temporary directory"
        echo "📥 Downloading $APP_NAME $KUBESOLO_VERSION..."
        curl -sfL "$BIN_URL" -o "$temp_dir/kubesolo.tar.gz" || handle_error "Failed to download $APP_NAME from $BIN_URL"

        echo "📦 Extracting $APP_NAME..."
        tar -xzf "$temp_dir/kubesolo.tar.gz" -C "$temp_dir" || handle_error "Failed to extract $APP_NAME archive"

        echo "📝 Installing binary..."
        mv "$temp_dir/kubesolo" "$INSTALL_PATH" || handle_error "Failed to move binary to $INSTALL_PATH"
        rm -rf "$temp_dir"
    fi

    chmod +x "$INSTALL_PATH" || handle_error "Failed to set executable permissions on $INSTALL_PATH"
}

# ── Script entry point ────────────────────────────────────────────────────────

# ── Recover env vars stripped by sudo env_reset ───────────────────────────────
# Some systems configure sudo with env_reset, causing '-E' to be silently
# ignored and stripping KUBESOLO_* vars before the script runs. Since we are
# invoked as root, we can read them back from the sudo process's own
# environment via /proc/$PPID/environ, which retains the pre-sudo shell env.
# Only triggers when running under sudo with PORTAINER_EDGE_KEY missing, and
# is a no-op on systems without procfs.
if [ -n "$SUDO_USER" ] && [ -z "$KUBESOLO_PORTAINER_EDGE_KEY" ] && [ -r "/proc/$PPID/environ" ]; then
    while IFS= read -r _kv; do
        [ -n "$_kv" ] && export "$_kv"
    done << _PENV
$(tr '\0' '\n' < "/proc/$PPID/environ" | grep '^KUBESOLO_[A-Z_]*=')
_PENV
    unset _kv
fi

# Default configuration from environment variables
KUBESOLO_VERSION="${KUBESOLO_VERSION:-v1.2.0}"
CONFIG_PATH="${KUBESOLO_PATH:-/var/lib/kubesolo}"
APISERVER_EXTRA_SANS="${KUBESOLO_APISERVER_EXTRA_SANS:-}"
PORTAINER_EDGE_ID="${KUBESOLO_PORTAINER_EDGE_ID:-}"
PORTAINER_EDGE_KEY="${KUBESOLO_PORTAINER_EDGE_KEY:-}"
PORTAINER_EDGE_ASYNC="${KUBESOLO_PORTAINER_EDGE_ASYNC:-false}"
PORTAINER_EDGE_IMAGE="${KUBESOLO_PORTAINER_EDGE_IMAGE:-}"
LOAD_BALANCER="${KUBESOLO_LOAD_BALANCER:-true}"
LOCAL_STORAGE="${KUBESOLO_LOCAL_STORAGE:-true}"
LOCAL_STORAGE_SHARED_PATH="${KUBESOLO_LOCAL_STORAGE_SHARED_PATH:-}"
DB_WAL_REPAIR="${KUBESOLO_DB_WAL_REPAIR:-false}"
DISABLE_IPV6="${KUBESOLO_DISABLE_IPV6:-false}"
CPU_MANAGER_POLICY="${KUBESOLO_CPU_MANAGER_POLICY:-none}"
CPU_MANAGER_POLICY_OPTIONS="${KUBESOLO_CPU_MANAGER_POLICY_OPTIONS:-}"
RESERVED_CPUS="${KUBESOLO_RESERVED_CPUS:-}"
SYSTEM_RESERVED="${KUBESOLO_SYSTEM_RESERVED:-}"
STARTUP_TIMEOUT="${KUBESOLO_STARTUP_TIMEOUT:-600}"
D2K="${KUBESOLO_D2K:-false}"
D2K_NAMESPACE="${KUBESOLO_D2K_NAMESPACE:-d2k}"
DEBUG="${KUBESOLO_DEBUG:-false}"
PPROF_SERVER="${KUBESOLO_PPROF_SERVER:-false}"
RUN_MODE="${KUBESOLO_RUN_MODE:-service}"  # service, foreground, or daemon
PROXY="${KUBESOLO_PROXY:-}"
KUBESOLO_OFFLINE_INSTALL="${KUBESOLO_OFFLINE_INSTALL:-}"
DOWNLOAD_ONLY_DIR="${KUBESOLO_DOWNLOAD_DIR:-}"
INSTALL_PREREQS="${KUBESOLO_INSTALL_PREREQS:-false}"
OFFLINE="${KUBESOLO_OFFLINE:-false}"

# Parse command line arguments
for arg in "$@"; do
  case $arg in
    --version=*)
      KUBESOLO_VERSION="${arg#*=}"
      ;;
    --path=*)
      CONFIG_PATH="${arg#*=}"
      ;;
    --apiserver-extra-sans=*)
      APISERVER_EXTRA_SANS="${arg#*=}"
      ;;
    --portainer-edge-id=*)
      PORTAINER_EDGE_ID="${arg#*=}"
      ;;
    --portainer-edge-key=*)
      PORTAINER_EDGE_KEY="${arg#*=}"
      ;;
    --portainer-edge-async=*)
      PORTAINER_EDGE_ASYNC="${arg#*=}"
      ;;
    --portainer-edge-image=*)
      PORTAINER_EDGE_IMAGE="${arg#*=}"
      ;;
    --local-storage=*)
      LOCAL_STORAGE="${arg#*=}"
      ;;
    --d2k=*)
      D2K="${arg#*=}"
      ;;
    --d2k-namespace=*)
      D2K_NAMESPACE="${arg#*=}"
      ;;
    --debug=*)
      DEBUG="${arg#*=}"
      ;;
    --pprof-server=*)
      PPROF_SERVER="${arg#*=}"
      ;;
    --cpu-manager-policy=*)
      CPU_MANAGER_POLICY="${arg#*=}"
      ;;
    --cpu-manager-policy-options=*)
      CPU_MANAGER_POLICY_OPTIONS="${arg#*=}"
      ;;
    --reserved-cpus=*)
      RESERVED_CPUS="${arg#*=}"
      ;;
    --system-reserved=*)
      SYSTEM_RESERVED="${arg#*=}"
      ;;
    --run-mode=*)
      RUN_MODE="${arg#*=}"
      ;;
    --proxy=*)
      PROXY="${arg#*=}"
      ;;
    --offline-install=*)
      KUBESOLO_OFFLINE_INSTALL="${arg#*=}"
      ;;
    --offline)
      OFFLINE="true"
      ;;
    --install-prereqs)
      INSTALL_PREREQS="true"
      ;;
    --download-only)
      DOWNLOAD_ONLY_DIR="."
      ;;
    --download-only=*)
      DOWNLOAD_ONLY_DIR="${arg#*=}"
      ;;
    --help)
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  --version=VERSION            Set KubeSolo version (default: $KUBESOLO_VERSION)"
      echo "  --path=PATH                  Set configuration path (default: $CONFIG_PATH)"
      echo "  --apiserver-extra-sans=SANS  Set additional Subject Alternative Names for the API server"
      echo "  --portainer-edge-id=ID       Set Portainer Edge ID"
      echo "  --portainer-edge-key=KEY     Set Portainer Edge Key"
      echo "  --portainer-edge-async=true|false   Enable Portainer Edge Async (default: $PORTAINER_EDGE_ASYNC)"
      echo "  --portainer-edge-image=IMAGE        Set the Portainer Edge Agent image (default: docker.io/portainer/agent:lts)"
      echo "  --local-storage=true|false   Enable local storage (default: $LOCAL_STORAGE)"
      echo "  --d2k=true|false             Embed d2k Docker-to-Kubernetes API translator (default: $D2K)"
      echo "  --d2k-namespace=NAMESPACE    Namespace into which d2k is deployed (default: $D2K_NAMESPACE)"
      echo "  --debug=true|false           Enable debug logging (default: $DEBUG)"
      echo "  --pprof-server=true|false    Enable pprof server (default: $PPROF_SERVER)"
      echo "  --cpu-manager-policy=POLICY  none or static; static gives Guaranteed pods exclusive cores (default: $CPU_MANAGER_POLICY)"
      echo "  --cpu-manager-policy-options=OPTS   Comma-separated key=value options for the static policy"
      echo "  --reserved-cpus=CPUSET       CPUs reserved for the host, e.g. 0 or 0-1 (default: 0 when the static policy is used)"
      echo "  --system-reserved=LIST       Resources withheld from allocatable, e.g. cpu=1,memory=500Mi"
      echo "  --run-mode=MODE              Run mode: service, foreground, or daemon (default: $RUN_MODE)"
      echo "  --proxy=URL                  Set proxy for HTTP/HTTPS requests"
      echo "  --offline                    Download the offline build (all images embedded, for air-gapped environments)"
      echo "  --offline-install=PATH       Use a local binary or archive instead of downloading"
      echo "  --download-only[=DIR]        Download binary archive and install script for offline use (default dir: .)"
      echo "  --install-prereqs            Automatically install missing prerequisites (e.g. nftables on Alpine)"
      echo "  --help                       Show this help message"
      echo ""
      echo "Supported Init Systems: systemd, sysvinit, s6, runit, openrc, upstart"
      echo "Fallback modes: foreground (manual start), daemon (background process)"
      exit 0
      ;;
  esac
done

# ── Download-only path ────────────────────────────────────────────────────────
# Runs without root. No pre-flight checks, no installation.
if [ -n "$DOWNLOAD_ONLY_DIR" ]; then
    download_bundle "$DOWNLOAD_ONLY_DIR"
    exit 0
fi

# ── Installation path ─────────────────────────────────────────────────────────

# Root is required for installation
if [ "$(id -u)" -ne 0 ]; then
    echo "❌ Error: This script must be run as root or via sudo"
    echo "   Please run: sudo $0 $*"
    exit 1
fi

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case $ARCH in
    x86_64)
        ARCH="amd64"
        ;;
    aarch64)
        ARCH="arm64"
        ;;
    armv7l)
        ARCH="arm"
        ;;
    riscv64)
        ARCH="riscv64"
        ;;
    *)
        handle_error "Unsupported architecture: $ARCH"
        ;;
esac

# Detect libc type (glibc vs musl)
LIBC_SUFFIX=""
if [ -f /lib/ld-musl-*.so.1 ] || [ -f /usr/lib/ld-musl-*.so.1 ]; then
    # Check if musl builds are available for this architecture
    if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "arm64" ]; then
        LIBC_SUFFIX="-musl"
        echo "🔍 Detected musl libc system"
    else
        handle_error "musl libc detected but musl builds are only available for amd64 and arm64 architectures. Current architecture: $ARCH"
    fi
else
    echo "🔍 Detected glibc system"
fi

# Detect init system and environment
INIT_SYSTEM=$(detect_init_system)
ENVIRONMENT=$(detect_environment)

echo "🔍 Detected init system: $INIT_SYSTEM"
echo "🔍 Detected environment: $ENVIRONMENT"

# Binary configuration
APP_NAME="kubesolo"
OFFLINE_SUFFIX=""
if [ "$OFFLINE" = "true" ]; then
    OFFLINE_SUFFIX="-offline"
    echo "🔍 Using offline build (air-gapped, all images embedded)"
fi
ARCHIVE_NAME="kubesolo-$KUBESOLO_VERSION-$OS-$ARCH$LIBC_SUFFIX$OFFLINE_SUFFIX.tar.gz"
BIN_URL="https://github.com/portainer/kubesolo/releases/download/$KUBESOLO_VERSION/$ARCHIVE_NAME"

# Pre-flight checks
check_docker_prerequisite
check_hostname_compliance
check_iptables_comment_module

# Check and optionally install nftables on Alpine
check_nftables

# Ensure Alpine cgroups service is enabled (required for cgroupv2 controllers)
ensure_alpine_cgroups_service

# Function to check for required cgroups controllers
check_cgroups

# Stop any running KubeSolo processes and clean up conflicts
stop_running_processes
stop_port_processes
cleanup_file_conflicts

# Service configuration
INSTALL_PATH="/usr/local/bin/$APP_NAME"

echo "🔄 Installing $APP_NAME $KUBESOLO_VERSION for $INIT_SYSTEM init system..."

install_binary

# Handle SELinux file contexts if SELinux tools are available
if command -v restorecon >/dev/null 2>&1; then
    echo "🔒 Restoring SELinux contexts for installed binary..."
    restorecon -v "$INSTALL_PATH" || echo "⚠️  Could not restore SELinux context (this may be normal)"
fi

# Construct command arguments
CMD_ARGS="--path=$CONFIG_PATH"

if [ -n "$APISERVER_EXTRA_SANS" ]; then
  CMD_ARGS="$CMD_ARGS --apiserver-extra-sans=$APISERVER_EXTRA_SANS"
fi

if [ -n "$PORTAINER_EDGE_ID" ]; then
  CMD_ARGS="$CMD_ARGS --portainer-edge-id=$PORTAINER_EDGE_ID"
fi

if [ -n "$PORTAINER_EDGE_KEY" ]; then
  CMD_ARGS="$CMD_ARGS --portainer-edge-key=$PORTAINER_EDGE_KEY"
fi

if [ "$PORTAINER_EDGE_ASYNC" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --portainer-edge-async=true"
fi

if [ -n "$PORTAINER_EDGE_IMAGE" ]; then
  CMD_ARGS="$CMD_ARGS --portainer-edge-image=$PORTAINER_EDGE_IMAGE"
fi

if [ "$LOAD_BALANCER" = "false" ]; then
  CMD_ARGS="$CMD_ARGS --load-balancer=false"
fi

if [ "$LOCAL_STORAGE" = "false" ]; then
  CMD_ARGS="$CMD_ARGS --local-storage=false"
fi

if [ -n "$LOCAL_STORAGE_SHARED_PATH" ]; then
  CMD_ARGS="$CMD_ARGS --local-storage-shared-path=$LOCAL_STORAGE_SHARED_PATH"
fi

if [ "$DB_WAL_REPAIR" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --db-wal-repair=true"
fi

if [ "$DISABLE_IPV6" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --disable-ipv6=true"
fi

if [ "$CPU_MANAGER_POLICY" != "none" ]; then
  CMD_ARGS="$CMD_ARGS --cpu-manager-policy=$CPU_MANAGER_POLICY"
fi

if [ -n "$CPU_MANAGER_POLICY_OPTIONS" ]; then
  CMD_ARGS="$CMD_ARGS --cpu-manager-policy-options=$CPU_MANAGER_POLICY_OPTIONS"
fi

if [ -n "$RESERVED_CPUS" ]; then
  CMD_ARGS="$CMD_ARGS --reserved-cpus=$RESERVED_CPUS"
fi

if [ -n "$SYSTEM_RESERVED" ]; then
  CMD_ARGS="$CMD_ARGS --system-reserved=$SYSTEM_RESERVED"
fi

if [ "$STARTUP_TIMEOUT" != "600" ]; then
  CMD_ARGS="$CMD_ARGS --startup-timeout=$STARTUP_TIMEOUT"
fi

if [ "$D2K" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --d2k=true --d2k-namespace=$D2K_NAMESPACE"
fi

if [ "$DEBUG" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --debug=$DEBUG"
fi

if [ "$PPROF_SERVER" = "true" ]; then
  CMD_ARGS="$CMD_ARGS --pprof-server=$PPROF_SERVER"
fi

# Main service creation logic
echo "📝 Setting up $APP_NAME service..."

case "$RUN_MODE" in
    "foreground")
        run_foreground
        ;;
    "daemon")
        run_daemon
        ;;
    "service"|*)
        case "$INIT_SYSTEM" in
            "systemd")
                create_systemd_service
                ;;
            "sysvinit")
                create_sysvinit_service
                ;;
            "openrc")
                create_openrc_service
                ;;
            "s6")
                create_s6_service
                ;;
            "runit")
                create_runit_service
                ;;
            "upstart")
                create_upstart_service
                ;;
            "unknown"|*)
                echo "⚠️  Unknown or unsupported init system: $INIT_SYSTEM"
                echo "🔄 Falling back to daemon mode..."
                run_daemon
                ;;
        esac
        ;;
esac

# Wait for kubesolo to start and generate kubeconfig
if [ "$RUN_MODE" != "foreground" ]; then
    echo "📋 Service status and logs:"
    case "$INIT_SYSTEM" in
        "systemd")
            echo "   Status: systemctl status $APP_NAME"
            echo "   Logs: journalctl -u $APP_NAME -f"
            ;;
        "sysvinit")
            if command -v service >/dev/null 2>&1; then
                echo "   Status: service $APP_NAME status"
            else
                echo "   Status: /etc/init.d/$APP_NAME status"
            fi
            echo "   Logs: tail -f /var/log/syslog | grep $APP_NAME"
            ;;
        "openrc")
            echo "   Status: rc-service $APP_NAME status"
            echo "   Logs: tail -f /var/log/messages | grep $APP_NAME"
            ;;
        *)
            echo "   Logs: tail -f /var/log/$APP_NAME.log"
            ;;
    esac
fi

# Check for kubectl and merge kubeconfig (same as original)
KUBECTL_PATH=""
if command -v kubectl >/dev/null 2>&1; then
    KUBECTL_PATH=$(command -v kubectl)
fi

if [ -n "$KUBECTL_PATH" ] && [ -x "$KUBECTL_PATH" ] && [ "$RUN_MODE" != "foreground" ]; then
    echo "🔍 Detected kubectl installation at $KUBECTL_PATH"

    # Detect real user when running under sudo
    REAL_USER="$USER"
    REAL_HOME="$HOME"
    REAL_UID=""
    REAL_GID=""

    if [ -n "$SUDO_USER" ] && [ "$SUDO_USER" != "root" ]; then
        REAL_USER="$SUDO_USER"
        REAL_UID="$SUDO_UID"
        REAL_GID="$SUDO_GID"
        # Get the real user's home directory
        REAL_HOME=$(getent passwd "$SUDO_USER" | cut -d: -f6)
        if [ -z "$REAL_HOME" ]; then
            REAL_HOME="/home/$SUDO_USER"
        fi
        echo "🔍 Detected sudo usage - using real user: $REAL_USER (home: $REAL_HOME)"
    fi

    # Wait for kubesolo to generate the kubeconfig
    echo "⏳ Waiting for kubesolo to generate kubeconfig..."
    i=1
    while [ $i -le 30 ]; do
        if [ -f "$CONFIG_PATH/pki/admin/admin.kubeconfig" ]; then
            break
        fi
        if [ $i -eq 30 ]; then
            echo "⚠️  Kubeconfig not found in $CONFIG_PATH/pki/admin/admin.kubeconfig after waiting"
            exit 0
        fi
        sleep 1
        i=$((i + 1))
    done

    if [ -f "$CONFIG_PATH/pki/admin/admin.kubeconfig" ]; then
        echo "🔄 Merging kubeconfig..."

        # Create backup of existing kubeconfig
        if [ -f "$REAL_HOME/.kube/config" ]; then
            cp "$REAL_HOME/.kube/config" "$REAL_HOME/.kube/config.backup-$(date +%Y%m%d%H%M%S)" || handle_error "Failed to backup existing kubeconfig"
        fi

        # Create .kube directory if it doesn't exist
        mkdir -p "$REAL_HOME/.kube" || handle_error "Failed to create .kube directory"

        # Merge the configs
        export KUBECONFIG="$REAL_HOME/.kube/config:$CONFIG_PATH/pki/admin/admin.kubeconfig"
        if "$KUBECTL_PATH" config view --flatten > "$REAL_HOME/.kube/config.tmp" 2>/dev/null; then
            mv "$REAL_HOME/.kube/config.tmp" "$REAL_HOME/.kube/config" || handle_error "Failed to update kubeconfig"
            echo "✅ Kubeconfig merged successfully"
            echo "📝 Your existing kubeconfig has been backed up with timestamp"
        else
            echo "⚠️  Failed to merge kubeconfig, copying KubeSolo config as default"
            cp "$CONFIG_PATH/pki/admin/admin.kubeconfig" "$REAL_HOME/.kube/config" || handle_error "Failed to copy kubeconfig"
        fi
        unset KUBECONFIG

        # Fix ownership if we're running under sudo
        if [ -n "$SUDO_USER" ] && [ "$SUDO_USER" != "root" ] && [ -n "$REAL_UID" ] && [ -n "$REAL_GID" ]; then
            echo "🔧 Fixing kubeconfig ownership for user $REAL_USER..."
            chown -R "$REAL_UID:$REAL_GID" "$REAL_HOME/.kube" || echo "⚠️  Could not fix kubeconfig ownership (this may be normal)"
        fi

        echo "📁 Kubeconfig location: $REAL_HOME/.kube/config"
    fi
else
    if [ "$RUN_MODE" != "foreground" ]; then
        echo "ℹ️  kubectl not found, skipping kubeconfig merge"
        echo "💡 To use kubectl, please install it first: https://kubernetes.io/docs/tasks/tools/install-kubectl/"
        echo "📁 Kubeconfig location: $CONFIG_PATH/pki/admin/admin.kubeconfig"
    fi
fi

echo "✅ $APP_NAME installation completed!"

# Exit with success code
exit 0
