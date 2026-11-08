# sagemcom-wifi-guardian

**Read and change the Wi-Fi settings that the web page of Sagemcom F@st 5690 routers (Bell Giga Hub, and the
same box from other operators) does not show, and keep them in place when the router puts the old ones back.**
Windows, Linux, macOS. Single static binary, Go standard library only.

*Version française plus bas.*

> Unofficial tool, not affiliated with Sagemcom or any operator. Writing a wrong value can drop every Wi-Fi
> client until you put it back. Read before you write: `options`, `get`, `guard --once --dry-run`.

## What it does

- `options` lists useful hidden settings (beamforming, OFDMA, MU-MIMO, airtime fairness, GreenAP, channel switch
  announcement, channel width, channel blacklist, channel, transmit power, DTIM, 802.11k, multicast-to-unicast,
  security mode and MFP per network, network names), each with its xpath, what it does, the values, the risk,
  and its current value on your router.
- `get`, `set`, `dump` read and write any xpath of the router's API.
- `clients` shows the link rate of every Wi-Fi client: the only reliable proof that 160 MHz is really in use.
- `guard` checks a list of wanted settings every 5 minutes and writes them back when the router has reverted
  them (reboot, operator push over TR-069, firmware update).

Nothing about your network is built in: you type the router address, user and password once with `init`.

## Quick start

```
sagemcom-wifi-guardian init                     # address (default 192.168.2.1), user (admin), password
sagemcom-wifi-guardian options                  # catalogue + current values
sagemcom-wifi-guardian clients                  # who is connected, at what rate
sagemcom-wifi-guardian get "Device/WiFi/Radios/Radio[Alias='RADIO5G']/Channel"
sagemcom-wifi-guardian set "Device/WiFi/Radios/Radio[Alias='RADIO5G']/TransmitBeamForming" True
sagemcom-wifi-guardian dump Device/WiFi/SSIDs   # a subtree as JSON, passwords and keys masked
sagemcom-wifi-guardian guard --once --dry-run   # what guard would change, without writing
sagemcom-wifi-guardian guard                    # loop: check every 5 min, correct
```

Binaries: `dist/` (Windows amd64, Linux amd64 and arm64, macOS arm64) or build with Go ≥ 1.22: `go build .`

Options, given **before** the command arguments (`get --config f.json <xpath>`):

| option | commands | meaning |
|---|---|---|
| `--config <file>` | all | credentials file (default: `<user config dir>/sagemcom-wifi-guardian/config.json`) |
| `--settings <file>` | guard | wanted settings (default: `settings.json` next to the config file) |
| `--once` | guard | one round, then exit (exit code 1 on error) |
| `--dry-run` | guard | read and report differences, never write |
| `--interval 5m` | guard | time between rounds |
| `--log <file>` | guard | append the log to a file instead of standard error |

### Password

- `init` asks for it without echo, tests it, and stores it in `config.json` with mode **0600** (Unix).
- **Windows ignores that mode.** Restrict the file (or its folder) to SYSTEM, Administrators and you:
  `icacls "C:\path\config.json" /inheritance:r /grant:r "%USERNAME%:F" "*S-1-5-18:F" "*S-1-5-32-544:F"`
  (`init` prints the exact command).
- Or give it in the environment: `SAGEMCOM_PASSWORD` (also `SAGEMCOM_HOST`, `SAGEMCOM_USER`); they override the
  file, and no file is needed then.
- It is **never** read from the command line, where any user could see it in the process list.

## Default settings (`settings.example.jsonc`)

`init` writes this file as `settings.json` next to `config.json`. It is JSON with `//` comments; edit it as you
like. Each rule is `name`, `xpath`, `value` (sent as text, exactly as written) and optionally `wait`.
These are the settings validated at home on a Giga Hub in **Canada** (`RegulatoryRegionSubRegion` = `CA/0`).
Channel numbers depend on the country: check `PossibleChannels` with `options` first.

| rule | value | why |
|---|---|---|
| 5 GHz `BlackListedChannels` | `104,108,112,116,136,140,144,149,153,161,165` | see below |
| 5 GHz `AutoChannelEnable` | `False` | otherwise the router leaves the chosen channel |
| 5 GHz `OperatingChannelBandwidth` | `160MHz` | the unit is required |
| 5 GHz `Channel` | `44`, wait 35 min | see DFS below |
| 5 GHz `TransmitBeamForming` | `True` | was off on 5 GHz only |
| 6 GHz `GreenAPEnabled` | `False` | power saving was on for 6 GHz only |
| 6 GHz network name (optional, commented out) | main name + `-6G` | see below |

**Why 36 to 64 are removed from the blacklist.** The factory list was
`36,40,48,52,56,64,104,108,112,116,136,140,144,149,153,161,165`. A 160 MHz channel on 5 GHz needs a whole block
of eight 20 MHz channels: 36–64 or 100–128. 100–128 is already broken by 104–116, so with 36–64 blacklisted too
no block is left and **the router silently stays at 80 MHz**, whatever the width setting says. Removing 36–64
from the list is what makes 160 MHz apply.

**Why the channel waits 35 minutes.** Channel 44 at 160 MHz spans 36–64, whose upper half (52–64) is DFS. When
the radio detects a radar it must leave, and the channel is forbidden for 30 minutes. The guard waits until the
difference has lasted 35 minutes before going back.

**Why a separate 6 GHz name.** With one name everywhere, the network is WPA2 on 2.4/5 GHz and WPA3 with mandatory
MFP (802.11w) on 6 GHz, as the 6 GHz standard requires. iPhones then hop between bands under the same name and
get refused (an "incorrect login" error). At home **90 % of 6 GHz connections lasted under 10 seconds**. A
separate name (`<main name>-6G`, read from the main network when guard starts) lets 6 GHz devices join it on
purpose. To enable it, uncomment the rule at the end of the file and check the SSID aliases with
`dump Device/WiFi/SSIDs` (on our unit: `WL_PRIV` for the main network, `WL_PRIV_6G` for 6 GHz). If you rename
the main network later, restart the guard.

## Traps (all handled by the tool)

- **`getDownloadURI` crashes the router.** Three calls on the same morning gave three crashes: HTTP 500, then 503
  on everything, then a watchdog reset and about **12 minutes without Internet or Wi-Fi** each time. The client
  therefore **refuses any API method other than `logIn`, `logOut`, `getValue` and `setValue`**, before anything
  is sent (whitelist in `xmo.go`, covered by a test). Do not remove it.
- **HTTP 500 or 503: stop at once.** The router may be about to crash. The tool sends nothing more, not even the
  log-out; `guard` then leaves the router alone for 15 minutes.
- **One admin session at a time.** While the web page, the phone app or another tool is logged in, the login is
  refused. The tool retries (3 times), keeps sessions short, and always logs out.
- **`160` is refused, `160MHz` works.** The router lists the widths as `20,40,AUTO,80,160` but answers
  `XMO_ENUM_ERR` to `160`; it then reads back `160MHZ`. Comparisons ignore case (except network names).
- **`CurrentOperatingChannelBandwidth` is not proof.** We saw it stuck at `80MHz` while 160 MHz was working (it
  reads `160MHz` at other times). The proof is the clients' link rate
  (`Device/WiFi/AccessPoints/AccessPoint/AssociatedDevices/.../LastDataDownlinkRate`, in kbit/s): a 2-stream
  Wi-Fi 6 client cannot exceed 1201 Mbit/s at 80 MHz. `clients` does that check.

## How `guard` decides

- Rules are applied in file order (on 5 GHz: blacklist, auto channel, width, channel), 5 s apart.
- At most **one correction per rule per hour**: if the router keeps reverting, the guard does not fight it.
- A rule with `wait` is corrected only after the difference has lasted that long.
- No correction while the router has been up for less than 5 minutes.
- `state.json` (next to the config file) keeps the timers across restarts and the last values read.
- The log says `CORRECTED`, `DIFF` (with the reason for not correcting), `ERROR`, and `OK` once an hour.

## Running as a service

### Windows: scheduled task, SYSTEM, at start-up

In an elevated PowerShell:

```powershell
$dir = 'C:\ProgramData\sagemcom-wifi-guardian'
New-Item -ItemType Directory -Force $dir | Out-Null
Copy-Item .\sagemcom-wifi-guardian-windows-amd64.exe "$dir\sagemcom-wifi-guardian.exe"
# only SYSTEM and Administrators may read the folder (the password goes there)
icacls $dir /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'
& "$dir\sagemcom-wifi-guardian.exe" init --config "$dir\config.json"
notepad "$dir\settings.json"          # review the wanted settings
& "$dir\sagemcom-wifi-guardian.exe" guard --config "$dir\config.json" --once --dry-run

$a = New-ScheduledTaskAction -Execute "$dir\sagemcom-wifi-guardian.exe" `
    -Argument "guard --config `"$dir\config.json`" --log `"$dir\guard.log`""
$s = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 `
    -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName SagemcomWifiGuardian -Action $a -Trigger (New-ScheduledTaskTrigger -AtStartup) `
    -Settings $s -Principal (New-ScheduledTaskPrincipal -UserId SYSTEM -LogonType ServiceAccount -RunLevel Highest)
Start-ScheduledTask SagemcomWifiGuardian
```

Uninstall: `Stop-ScheduledTask SagemcomWifiGuardian; Unregister-ScheduledTask SagemcomWifiGuardian`.

### Linux: systemd

```sh
sudo install -m 755 sagemcom-wifi-guardian-linux-amd64 /usr/local/bin/sagemcom-wifi-guardian
sudo sagemcom-wifi-guardian init --config /etc/sagemcom-wifi-guardian/config.json   # file 0600, folder 0700
sudoedit /etc/sagemcom-wifi-guardian/settings.json
sudo sagemcom-wifi-guardian guard --config /etc/sagemcom-wifi-guardian/config.json --once --dry-run
```

`/etc/systemd/system/sagemcom-wifi-guardian.service`:

```ini
[Unit]
Description=Sagemcom Wi-Fi guardian (keeps router Wi-Fi settings in place)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/sagemcom-wifi-guardian guard --config /etc/sagemcom-wifi-guardian/config.json
Restart=always
RestartSec=60
ProtectSystem=strict
ReadWritePaths=/etc/sagemcom-wifi-guardian
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
```

`sudo systemctl daemon-reload && sudo systemctl enable --now sagemcom-wifi-guardian`, then
`journalctl -u sagemcom-wifi-guardian -f`. To keep the password out of the file, leave `password` empty and add
`EnvironmentFile=/etc/sagemcom-wifi-guardian/env` (mode 0600, line `SAGEMCOM_PASSWORD=...`).

## Compatibility

Tested on a **F@st 5690 (Bell Giga Hub), firmware 2.14.2**, in Canada. The protocol code and the guard logic
come from a private version that has been logging in, reading and writing on that router every 5 minutes; this
public tool itself was checked there read-only (`options`, `clients`, `get`, `dump`, `guard --once --dry-run`). Other operators' versions of the box should speak the same API but are untested; their
aliases may differ (`clients`, `options` and `dump` discover access points and networks by reading the router).
Radio aliases used: `RADIO2G4`, `RADIO5G`, `RADIO6G`.

## Build and test

```
go vet ./...
go test ./...
GOOS=windows GOARCH=amd64 go build -trimpath -o sagemcom-wifi-guardian.exe .
```

Tests cover the authentication key (vector computed with the Python library), the decision logic, the method
whitelist, the HTTP 5xx stop, the settings parser, secret masking, and a full guard round against a fake router.

The protocol (SHA-512 login, nonce, per-request auth key) is ported from the Python library
[sagemcom_api](https://github.com/iMicknl/python-sagemcom-api) 1.5.0 (MIT).

---

## Français

**Lire et modifier les réglages Wi-Fi que la page web des routeurs Sagemcom F@st 5690 (Bell Giga Hub, et le même
boîtier chez d'autres opérateurs) ne montre pas, et les garder en place quand le routeur remet les anciens.**
Windows, Linux, macOS. Un seul binaire, uniquement la bibliothèque standard de Go.

> Outil non officiel, sans lien avec Sagemcom ni avec un opérateur. Une mauvaise valeur peut déconnecter tous les
> appareils Wi-Fi jusqu'à ce qu'on la corrige. Lisez avant d'écrire : `options`, `get`, `guard --once --dry-run`.

### Ce que fait l'outil

- `options` : catalogue des réglages cachés utiles (beamforming, OFDMA montant et descendant, MU-MIMO descendant,
  équité de temps d'antenne ATF, GreenAP, annonce de changement de canal CSA, largeur de canal, liste noire des
  canaux, canal et canal automatique, puissance d'émission et `IncreasedPowerEnable`, DTIM, rapports de voisinage
  802.11k, `DirectMulticast`, mode de sécurité et MFP par point d'accès, nom du réseau par bande). Pour chacun :
  le xpath, une description, les valeurs possibles, le risque, et la valeur actuelle si l'outil est connecté.
- `get`, `set`, `dump` : lire et écrire n'importe quel xpath de l'API du routeur ; `dump` masque les mots de
  passe, clés et NIP.
- `clients` : vitesse de liaison de chaque appareil Wi-Fi, seule preuve fiable que le 160 MHz est actif.
- `guard` : vérifie toutes les 5 minutes une liste de réglages voulus et les réécrit quand le routeur les a
  remis à l'ancienne valeur (redémarrage, opérateur via TR-069, mise à jour du firmware).

Rien n'est codé en dur : on saisit l'adresse du routeur, l'utilisateur et le mot de passe une seule fois avec
`init`.

### Démarrage rapide

```
sagemcom-wifi-guardian init                     # adresse (192.168.2.1 par défaut), utilisateur (admin), mot de passe
sagemcom-wifi-guardian options                  # catalogue et valeurs actuelles
sagemcom-wifi-guardian clients                  # qui est connecté, à quelle vitesse
sagemcom-wifi-guardian get "Device/WiFi/Radios/Radio[Alias='RADIO5G']/Channel"
sagemcom-wifi-guardian set "Device/WiFi/Radios/Radio[Alias='RADIO5G']/TransmitBeamForming" True
sagemcom-wifi-guardian dump Device/WiFi/SSIDs   # sous-arbre en JSON, secrets masqués
sagemcom-wifi-guardian guard --once --dry-run   # ce que le gardien changerait, sans rien écrire
sagemcom-wifi-guardian guard                    # boucle : vérifie toutes les 5 min et corrige
```

Les options se placent **avant** les arguments : `--config`, `--settings`, `--once`, `--dry-run`, `--interval`,
`--log` (voir le tableau de la partie anglaise).

### Mot de passe

- `init` le demande sans l'afficher, le teste, puis l'enregistre dans `config.json` avec les droits **0600**
  sous Unix.
- **Windows ignore ces droits** : restreignez le fichier (ou son dossier) à SYSTEM, aux Administrateurs et à
  vous, avec `icacls` (`init` affiche la commande exacte ; exemple complet dans la section « Windows »).
- Ou passez-le par l'environnement : `SAGEMCOM_PASSWORD` (et `SAGEMCOM_HOST`, `SAGEMCOM_USER`), prioritaires sur
  le fichier.
- Jamais en argument de ligne de commande : n'importe quel utilisateur le verrait dans la liste des processus.

### Réglages par défaut

`init` écrit `settings.json` à côté de `config.json`, à partir de `settings.example.jsonc` (JSON avec commentaires
`//`). Ce sont les réglages validés chez nous sur un Giga Hub au **Canada** (`RegulatoryRegionSubRegion` =
`CA/0`) ; les canaux permis varient selon le pays, vérifiez `PossibleChannels` avec `options`.

- 5 GHz : `BlackListedChannels=104,108,112,116,136,140,144,149,153,161,165`, `AutoChannelEnable=False`,
  `OperatingChannelBandwidth=160MHz`, `Channel=44` (attente de 35 min), `TransmitBeamForming=True`.
- 6 GHz : `GreenAPEnabled=False`.
- Option (commentée) : nom séparé pour le 6 GHz, nom principal + `-6G`, calculé au démarrage du gardien.

**Pourquoi retirer 36 à 64 de la liste noire.** La liste d'origine était
`36,40,48,52,56,64,104,108,112,116,136,140,144,149,153,161,165`. Un canal de 160 MHz en 5 GHz exige un bloc
complet de huit canaux de 20 MHz : 36-64 ou 100-128. Le bloc 100-128 est déjà coupé par 104-116 ; si 36-64 est
aussi interdit, il ne reste aucun bloc et **le routeur reste à 80 MHz sans rien dire**, quel que soit le réglage
de largeur. Sans ce retrait, le 160 MHz ne s'applique pas.

**Pourquoi attendre 35 minutes pour le canal.** Le canal 44 en 160 MHz couvre 36-64, dont la moitié haute (52-64)
est soumise au DFS. Quand la radio détecte un radar, elle doit partir et le canal est interdit pendant
30 minutes. Le gardien attend que l'écart dure 35 minutes avant d'y revenir.

**Pourquoi un nom séparé pour le 6 GHz.** Avec un seul nom, le réseau est en WPA2 sur 2,4/5 GHz et en WPA3 avec
MFP obligatoire sur 6 GHz, comme l'exige la norme 6 GHz. Les iPhone basculent alors d'une bande à l'autre sous le
même nom et se font refuser (« incorrect login »). Chez nous, **90 % des connexions 6 GHz duraient moins de
10 secondes**. Avec un nom à part (`<nom principal>-6G`), seuls les appareils qu'on y inscrit l'utilisent. Pour
l'activer, décommentez la règle à la fin du fichier et vérifiez les alias avec `dump Device/WiFi/SSIDs`.

### Pièges (tous pris en charge par l'outil)

- **`getDownloadURI` fait planter le routeur.** Trois appels le même matin, trois plantages : HTTP 500, puis 503
  partout, puis réinitialisation par le chien de garde et environ **12 minutes sans Internet ni Wi-Fi** à chaque
  fois. L'outil **refuse donc toute méthode autre que `logIn`, `logOut`, `getValue` et `setValue`**, avant
  d'envoyer quoi que ce soit (liste blanche dans `xmo.go`, vérifiée par un test). Ne la retirez pas.
- **HTTP 500 ou 503 : arrêt immédiat.** Le routeur est peut-être en train de planter. L'outil n'envoie plus rien,
  pas même la déconnexion ; `guard` laisse ensuite le routeur tranquille 15 minutes.
- **Une seule session admin à la fois.** Si la page web, l'application mobile ou un autre outil est connecté, la
  connexion est refusée. L'outil réessaie (3 fois), garde des sessions courtes et se déconnecte toujours.
- **`160` est refusé, `160MHz` est accepté.** Le routeur annonce `20,40,AUTO,80,160` mais répond `XMO_ENUM_ERR`
  à `160`, et relit ensuite `160MHZ`. Les comparaisons ignorent la casse, sauf pour les noms de réseau.
- **`CurrentOperatingChannelBandwidth` ne prouve rien.** On l'a vu rester à `80MHz` alors que le 160 MHz
  fonctionnait (il affiche `160MHz` à d'autres moments). La preuve, c'est la vitesse de liaison des clients
  (`LastDataDownlinkRate` dans `AssociatedDevices`, en kbit/s) : un client Wi-Fi 6 à 2 flux ne dépasse pas
  1201 Mbit/s en 80 MHz. `clients` fait ce contrôle.

### Comment le gardien décide

Règles appliquées dans l'ordre du fichier, à 5 s d'intervalle ; au plus **une correction par règle et par
heure** ; `wait` = durée minimale de l'écart avant correction ; aucune correction si le routeur a démarré depuis
moins de 5 minutes ; `state.json` garde les minuteries entre deux redémarrages. Journal : `CORRECTED`, `DIFF`
(avec la raison), `ERROR`, et `OK` une fois par heure.

### Installation en service

- **Windows** : tâche planifiée au démarrage, compte SYSTEM, dossier `C:\ProgramData\sagemcom-wifi-guardian`
  restreint à SYSTEM et aux Administrateurs par `icacls`. Script PowerShell complet dans la section « Windows »
  ci-dessus.
- **Linux** : unité systemd ci-dessus (`/etc/sagemcom-wifi-guardian/config.json` en 0600, ou mot de passe dans un
  `EnvironmentFile` en 0600).

### Compatibilité

Testé sur un **F@st 5690 (Bell Giga Hub), firmware 2.14.2**, au Canada. Le protocole et la logique du gardien
viennent d'une version privée qui lit et écrit sur ce routeur toutes les 5 minutes ; l'outil public lui-même y a
été vérifié en lecture seule (`options`, `clients`, `get`, `dump`, `guard --once --dry-run`). Les versions du boîtier d'autres
opérateurs devraient parler la même API mais n'ont pas été testées ; leurs alias peuvent différer (`clients`,
`options` et `dump` découvrent les points d'accès en lisant le routeur).

## License

MIT, see [LICENSE](LICENSE).
