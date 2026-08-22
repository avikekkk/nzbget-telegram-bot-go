## Status of this document

The document describes methods available in NZBGet `version 13.0 and later`. The document is updated after a new stable version is out. Current testing version may have methods or fields not described here; these methods or fields may change before getting stable.

## Protocols

NZBGet supports `XML-RPC`, `JSON-RPC` and `JSON-P-RPC`. RPC-protocols allow to control the program from other applications. Many programming languages have libraries, which make the usage of `XML-RPC`, `JSON-RPC` and `JSON-P-RPC` very simple. The implementations of all three protocols in NZBGet are equal: all methods are identical. You may choose the protocol, which is better supported by your programming language.

## Authentication

NZBGet has three pairs of username/password:

- options `ControlUsername` and `ControlPassword` \- Full access, usually used when connecting to web-interface;
- options `RestrictedUsername` and `RestrictedPassword` \- `v15.0` Restricted user can control the program with few restrictions. He has access to web-interface and can see most program settings. He can not change program settings and can not view security related options or options provided by extension scripts. In terms of RPC-API the user:
  - cannot use method `saveconfig`;
  - methods `config` and `loadconfig` return string `***` for options those content is protected from the user.
- options `AddUsername` and `AddPassword` \- `v15.0` This user has only two permissions:
  - add new downloads using RPC-method `append`;
  - check program version using RPC-method `version`.

The RPC server uses HTTP basic authentication. The URL would be something like:

- for **XML-RPC**: `http://username:password@localhost:6789/xmlrpc`
- for **JSON-RPC**: `http://username:password@localhost:6789/jsonrpc`
- for **JSON-P-RPC**: `http://username:password@localhost:6789/jsonprp`

If HTTP basic authentication is somewhat problematic the username/password can also be passed in the URL as the first part of the path:

- `http://localhost:6789/username:password/xmlrpc`

`Security warning`: HTTP authentication is not secure. Although the password is encoded using Base64 it is not encrypted. For secure communication use HTTPS (needs to be explicitly enabled in NZBGet settings by user).

## Features and limitations

- Multicalls are supported for `XML-RPC` and not supported for `JSON-RPC`.
- Introspection is not supported.
- Only positional parameters are supported in `JSON-RPC`. Named parameters are not supported. Therefore parameter names are ignored but the order of parameters is important. All parameters are mandatory.
- Each call to `JSON-P-RPC` has one additional parameter - the name of callback-function. This parameter must be named `callback` and must be passed first (before any other parameters).
- 64 bit integers are returned in two separate fields `Hi` and `Lo` (for example `FileSizeHi` and `FileSizeLo`). These fields are unsigned 32 bit integers. Although dynamic languages such as PHP or Python have no problems with these fields the `XML-RPC` specification does not allow unsigned integers. This may cause troubles in statically typed languages such as Java or C++ if `XML-RPC`-parser expects only signed integers in 32 bit range. As a solution use `JSON-RPC` (which does allow unsigned integers) instead of `XML-RPC`.

## Program control

- [version](https://nzbget.com/documentation/api/version)
- [shutdown](https://nzbget.com/documentation/api/shutdown)
- [reload](https://nzbget.com/documentation/api/reload)

## Queue and history

- [listgroups](https://nzbget.com/documentation/api/listgroups)
- [listfiles](https://nzbget.com/documentation/api/listfiles)
- [history](https://nzbget.com/documentation/api/history)
- [append](https://nzbget.com/documentation/api/append)
- [editqueue](https://nzbget.com/documentation/api/editqueue)
- [scan](https://nzbget.com/documentation/api/scan)

## Status, logging and statistics

- [status](https://nzbget.com/documentation/api/status)
- [sysinfo](https://nzbget.com/documentation/api/sysinfo)
- [systemhealth](https://nzbget.com/documentation/api/systemhealth)
- [log](https://nzbget.com/documentation/api/log)
- [writelog](https://nzbget.com/documentation/api/writelog)
- [loadlog](https://nzbget.com/documentation/api/loadlog)
- [logscript](https://nzbget.com/documentation/api/logscript)
- [logupdate](https://nzbget.com/documentation/api/logupdate)
- [servervolumes](https://nzbget.com/documentation/api/servervolumes)
- [resetservervolume](https://nzbget.com/documentation/api/resetservervolume)

## Pause and speed limit

- [rate](https://nzbget.com/documentation/api/rate)
- [pausedownload](https://nzbget.com/documentation/api/pausedownload)
- [resumedownload](https://nzbget.com/documentation/api/resumedownload)
- [pausepost](https://nzbget.com/documentation/api/pausepost)
- [resumepost](https://nzbget.com/documentation/api/resumepost)
- [pausescan](https://nzbget.com/documentation/api/pausescan)
- [resumescan](https://nzbget.com/documentation/api/resumescan)
- [scheduleresume](https://nzbget.com/documentation/api/scheduleresume)

## Configuration

- [config](https://nzbget.com/documentation/api/config)
- [loadconfig](https://nzbget.com/documentation/api/loadconfig)
- [saveconfig](https://nzbget.com/documentation/api/saveconfig)
- [configtemplates](https://nzbget.com/documentation/api/configtemplates)

## Extensions

- [loadextensions](https://nzbget.com/documentation/api/loadextensions)
- [downloadextension](https://nzbget.com/documentation/api/downloadextension)
- [updateextension](https://nzbget.com/documentation/api/updateextension)
- [deleteextension](https://nzbget.com/documentation/api/deleteextension)

## Tests

- [testextension](https://nzbget.com/documentation/api/testextension)
- [testserver](https://nzbget.com/documentation/api/testserver)
- [testserverspeed](https://nzbget.com/documentation/api/testserverspeed)
- [testdiskspeed](https://nzbget.com/documentation/api/testdiskspeed)
- [testnetworkspeed](https://nzbget.com/documentation/api/testnetworkspeed)

#### Introduction

- [Feature Highlights](https://nzbget.com/documentation/feature-highlights/)

#### Installation manuals

- [Installation on Windows](https://nzbget.com/documentation/installation-on-windows/)
- [Installation on Windows via WinGet](https://nzbget.com/download/#windows)
- [Installation on macOS](https://nzbget.com/documentation/installation-on-mac/)
- [Installation on macOS via Homebrew](https://github.com/nzbgetcom/nzbget/blob/develop/osx/brew-info.md)
- [Installation on Linux](https://nzbget.com/documentation/installation-on-linux/)
- [Installation on Docker](https://nzbget.com/download/#docker)
- [Installation on Docker - LinuxServer.io version](https://nzbget.com/download/#docker)
- [Installation on Android](https://nzbget.com/documentation/installation-on-android/)
- [Installation on Synology](https://github.com/nzbgetcom/nzbget/blob/develop/docs/SYNOLOGY.md)
- [Installation on QNAP](https://github.com/nzbgetcom/nzbget/blob/develop/qnap/README.md)
- [Installation on other NAS devices](https://nzbget.com/download/#pills-others)

#### Building manuals

- [Building on Windows](https://nzbget.com/documentation/build-on-windows/)
- [Building on POSIX](https://nzbget.com/documentation/build-on-posix/)

#### Configuration

- [Backup And Restore Settings](https://nzbget.com/documentation/backup-and-restore-settings/)
- [Performance Tips](https://nzbget.com/documentation/performance-tips/)
- [Choosing Cipher](https://nzbget.com/documentation/choosing-cipher/)
- [Behind Other Web Server](https://nzbget.com/documentation/behind-other-web-server/)
- [TLS certificate verification](https://nzbget.com/documentation/certificate-verification/)
- [What is the Default NZBGet Password?](https://nzbget.com/documentation/what-is-the-default-nzbget-password/)
- [NZBGet Docker Setup Guide](https://nzbget.com/documentation/nzbget-docker-guide-installation-configuration/)
- [NZBGet Port Settings and Access Guide](https://nzbget.com/documentation/nzbget-port-settings-and-access-guide/)
- [How to Configure NZBGet: Complete Setup Guide](https://nzbget.com/documentation/how-to-configure-nzbget-complete-setup-guide/)
- [What Is Usenet?](https://nzbget.com/documentation/what-is-usenet/)
- [NZBGet 502 Error: Quick Fix Guide](https://nzbget.com/documentation/nzbget-502-error-quick-fix-guide/)
- [How to Upgrade from NZBGet v21.1 to the Latest NZBGet Version](https://nzbget.com/documentation/how-to-upgrade-from-nzbget-v21.1-to-the-latest-nzbget-version/)
- [Slow NZBGet Speeds? Here’s How to Fix It](https://nzbget.com/documentation/slow-nzbget-speeds-how-to-fix-it/)
- [NZBGet Performance Optimization Guide](https://nzbget.com/documentation/nzbget-performance-optimization-guide/)
- [NZBGet Path and Folder Structure Guide](https://nzbget.com/documentation/nzbget-path-and-folder-structure-guide/)
- [How to Link NZBGet with Sonarr, Radarr, and Other Tools](https://nzbget.com/documentation/how-to-link-nzbget-with-sonarr-radarr-and-other-tools/)
- [Sonarr vs VideoSort: How They Work with NZBGet](https://nzbget.com/documentation/sonarr-vs-videosort-automation-and-sorting-with-nzbget-explained/)
- [NZBGet Storage Guide: Improve Performance with Smarter Path Setup](https://nzbget.com/documentation/nzbget-storage-configuration-guide-best-directory-setup-for-speed/)
- [How to Configure NZBGet on Android TV](https://nzbget.com/documentation/how-to-configure-nzbget-on-android-tv/)
- [Using NZBGet with Sonarr and Radarr on Android TV](https://nzbget.com/documentation/using-nxbget-with-sonarr-and-radarr-on-android-tv/)

#### Performance tuning

- [How to Optimize Usenet Performance in NZBGet](https://nzbget.com/documentation/how-to-optimize-usenet-performance-in-nzbget/)
- [NZBGet SystemHealth: Configuration and Environment Diagnostics](https://nzbget.com/documentation/nzbget-systemhealth-guide-diagnose-setup-and-performance-issues/)
- [Testing Usenet Speed and Performance in NZBGet](https://nzbget.com/documentation/how-to-test-usenet-speed-in-nzbget/)
- [Diagnosing and Maximizing Usenet Speeds in NZBGet](https://nzbget.com/documentation/diagnosing-slow-usenet-speeds-in-nzbget/)
- [Understanding Download Health in NZBGet](https://nzbget.com/documentation/understanding-download-health-in-nzbget/)
- [Finding the Optimal Connection Count in NZBGet](https://nzbget.com/documentation/finding-the-optimal-connection-count-in-nzbget/)
- [Primary vs Fill Servers Explained](https://nzbget.com/documentation/primary-vs-fill-servers-explained/)
- [Understanding the Statistics Page in NZBGet](https://nzbget.com/documentation/understanding-the-statistics-page-in-nzbget/)

#### Usage

- [Quick Filter](https://nzbget.com/documentation/quick-filter/)
- [Keyboard Shortcuts](https://nzbget.com/documentation/keyboard-shortcuts/)
- [RSS and duplicate check](https://nzbget.com/documentation/rss/)
- [VideoSort](https://github.com/nzbgetcom/Extension-VideoSort)
- [Console Mode](https://nzbget.com/documentation/console-mode/)
- [Command Line Reference](https://nzbget.com/documentation/command-line-reference/)
- [Performing Speed Tests](https://nzbget.com/documentation/performing-speed-tests/)
- [NZBGet Interface Buttons Explained: What They Do and How to Use Them](https://nzbget.com/documentation/nzbget-interface-buttons-guide-what-they-do-how-to-use-them/)
- [How to Use the NZBGet Status Tab](https://nzbget.com/documentation/nzbget-status-tab-guide-monitor-test-and-troubleshoot-your-setup/)

#### Development

- [Building Development Version](https://nzbget.com/documentation/building-development-version/)
- [Cross Compiling](https://nzbget.com/documentation/cross-compiling/)
- [Packaging](https://nzbget.com/documentation/packaging/)
- [Functional Testing](https://nzbget.com/documentation/functional-testing/)
- [NServ NNTP server](https://nzbget.com/documentation/nserv-nntp-server/)

#### Extensions

- [Extension Scripts](https://nzbget.com/documentation/extension-scripts/)
- [Post-processing scripts](https://nzbget.com/documentation/post-processing-scripts/)
- [Scan Scripts](https://nzbget.com/documentation/scan-scripts/)
- [Queue Scripts](https://nzbget.com/documentation/queue-scripts/)
- [Scheduler Scripts](https://nzbget.com/documentation/scheduler-scripts/)
- [Feed Scripts](https://nzbget.com/documentation/feed-scripts/)
- [NZBGet Extension Manager: What It Is and How to Use It](https://nzbget.com/documentation/nzbget-extension-manager-how-to-install-and-manage-extensions/)
- [API reference](https://nzbget.com/documentation/api/)

#### News server setup

- [Astraweb](https://nzbget.com/documentation/astraweb/)
- [BlockNews](https://nzbget.com/documentation/blocknews/)
- [Cheapnews](https://nzbget.com/documentation/cheapnews/)
- [EasyNews](https://nzbget.com/documentation/easynews/)
- [Eweka](https://nzbget.com/documentation/eweka/)
- [FreeDiscussions](https://nzbget.com/documentation/freediscussions/)
- [Frugal Usenet](https://nzbget.com/documentation/frugalusenet/)
- [GigaNews](https://nzbget.com/documentation/giganews/)
- [NewsDemon](https://nzbget.com/documentation/newsdemon/)
- [Newsgroup Ninja](https://nzbget.com/documentation/newsgroupninja/)
- [NewsgroupDirect](https://nzbget.com/documentation/newsgroupdirect/)
- [Newshosting](https://nzbget.com/documentation/newshosting/)
- [Pure Usenet](https://nzbget.com/documentation/pureusenet/)
- [SunnyUsenet](https://nzbget.com/documentation/sunnyusenet/)
- [Supernews](https://nzbget.com/documentation/supernews/)
- [theCubeNet](https://nzbget.com/documentation/thecubenet/)
- [ThunderNews](https://nzbget.com/documentation/thundernews/)
- [TweakNews](https://nzbget.com/documentation/tweaknews/)
- [Usenet.Farm](https://nzbget.com/documentation/usenetfarm/)
- [UsenetExpress](https://nzbget.com/documentation/usenetexpress/)
- [UsenetServer](https://nzbget.com/documentation/usenetserver/)
- [UseNext](https://nzbget.com/documentation/usenext/)
- [ViperNews](https://nzbget.com/documentation/vipernews/)
- [XLned](https://nzbget.com/documentation/xlned/)
- [XS News](https://nzbget.com/documentation/xsnews/)

#### Other helpful guides

- [NZBGet vs SABnzbd: Side-by-Side Comparison](https://nzbget.com/documentation/nzbget-vs-sabnzbd-side-by-sides-comparison/)
- [NZBGet FAQ: Common Questions Answered](https://nzbget.com/documentation/nzbget-faq-setup-features-and-common-questions/)
- [How to Set Up NZBGet: Quickstart Guide for Beginners](https://nzbget.com/documentation/how-to-set-up-nzbget-quickstart-guide-for-beginners/)
- [NZBGet for Android: A Lightweight Usenet Android NZB Downloader and Setup Guide](https://nzbget.com/documentation/nzbget-for-android-a-lightweight-usenet-android-nzb-downloader-and-setup-guide/)

/js/scripts.min.js