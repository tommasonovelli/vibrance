# shellcheck shell=bash
# Shared by scripts/stack-smoke.sh and scripts/contract.sh, which run the
# real stack in a private install folder. Source it after common.sh; do not
# execute it.
#
# Defines: check_stack_model.

# The caller's shell must not steer the test. Compose's own settings would
# choose another project or other files. The settings of compose.yaml that
# name a volume or a host folder win over the test's .env when they are
# exported, and would turn a new volume of the test into a bind mount of a
# real folder, such as the data or the backups of an installation.
unset COMPOSE_PROJECT_NAME COMPOSE_FILE COMPOSE_PROFILES COMPOSE_ENV_FILES COMPOSE_PATH_SEPARATOR
unset MUSICLIB_DATA MUSICLIB_BACKUP MUSICLIB_IMPORT VIBRANCE_BACKUP

# check_stack_model MODEL PROJECT DIR: dies unless the interpolated model of
# `docker compose config` in the file MODEL is the project PROJECT and can
# touch nothing outside it: every container name is PROJECT or PROJECT-name,
# no port is published on the host, every bind mount is a path inside the
# install folder DIR (in the form docker understands), and every volume is a
# volume of the project (PROJECT_name), none external. Nothing has been
# started when it dies.
check_stack_model() {
  local model="$1" project="$2" dir="$3" name outside
  name="$(sed -n '1s/^name: //p' "${model}")"
  [[ "${name}" == "${project}" ]] || die "the Compose project is '${name}', not ${project}: nothing was started"
  if sed -n 's/^ *container_name: //p' "${model}" | grep -v "^${project}\(-[a-z]*\)\?\$"; then
    die "a container name above is not of the project ${project}: nothing was started"
  fi
  if grep -q '^ *published:' "${model}"; then
    die "the stack would publish a port on the host: nothing was started"
  fi
  # Docker Desktop prints a Windows path with backslashes; compare with
  # forward slashes.
  outside="$(awk -v dir="${dir//\\//}/" '
    /^ *- type: / { bind = ($3 == "bind"); next }
    bind && /^ *source: / {
      path = $0
      sub(/^ *source: /, "", path)
      gsub(/\\/, "/", path)
      if (index(path, dir) != 1) print path
      bind = 0
    }' "${model}")"
  [[ -z "${outside}" ]] || die "the stack would mount host folders outside its install folder (${outside//$'\n'/, }): nothing was started"
  if awk '/^[^ ]/ { section = $0 } section == "volumes:" && /^    name: / { print $2 }' "${model}" |
    grep -v "^${project}_"; then
    die "a volume above is not a volume of the project ${project}: nothing was started"
  fi
  if grep -q '^ *external: true' "${model}"; then
    die "the stack would use an external volume or network: nothing was started"
  fi
}
