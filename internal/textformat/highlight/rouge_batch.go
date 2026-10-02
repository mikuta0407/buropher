package highlight

// Rouge 4.7 の batchfile.rb の移植。

var (
	batKeywords      = wordset(`if else for in do goto call exit`)
	batOperatorWords = wordset(`exist defined errorlevel cmdextversion not equ neq lss leq gtr geq`)
	batDevices       = wordset(`con prn aux nul com1 com2 com3 com4 com5 com6 com7 com8 com9 lpt1 lpt2
lpt3 lpt4 lpt5 lpt6 lpt7 lpt8 lpt9`)
	batBuiltinCommands = wordset(`assoc attrib break bcdedit cacls cd chcp chdir chkdsk chkntfs choice
cls cmd color comp compact convert copy date del dir diskpart doskey
dpath driverquery echo endlocal erase fc find findstr format fsutil
ftype gpresult graftabl help icacls label md mkdir mklink mode more
move openfiles path pause popd print prompt pushd rd recover ren
rename replace rmdir robocopy setlocal sc schtasks shift shutdown sort
tree type ver verify vol xcopy waitfor wmic`)
	batOtherCommands = wordset(`addusers admodcmd ansicon arp at bcdboot bitsadmin browstat certreq
certutil change cidiag cipher cleanmgr clip cmdkey compress convertcp
coreinfo csccmd csvde cscript curl debug defrag delprof deltree devcon
diamond dirquota diruse diskshadow diskuse dism dnscmd dsacls dsadd
dsget dsquery dsmod dsmove dsrm dsmgmt dsregcmd edlin eventcreate
expand extract fdisk fltmc forfiles freedisk ftp getmac gpupdate
hostname ifmember inuse ipconfig kill lgpo lodctr logman logoff
logtime makecab mapisend mbsacli mem mountvol moveuser msg mshta
msiexec msinfo32 mstsc nbtstat net net1 netdom netsh netstat nlsinfo
nltest now nslookup ntbackup ntdsutil ntoskrnl ntrights nvspbind
pathping perms ping portqry powercfg pngout pnputil printbrm prncnfg
prnmngr procdump psexec psfile psgetsid psinfo pskill pslist
psloggedon psloglist pspasswd psping psservice psshutdown pssuspend
qbasic qgrep qprocess query quser qwinsta rasdial reg reg1 regdump
regedt32 regsvr32 regini reset restore rundll32 rmtshare route rpcping
run runas scandisk setspn setx sfc share shellrunas shortcut sigcheck
sleep slmgr strings subinacl sysmon telnet tftp tlist touch tracerpt
tracert tscon tsdiscon tskill tttracer typeperf tzutil undelete
unformat verifier vmconnect vssadmin w32tm wbadmin wecutil wevtutil
wget where whoami windiff winrm winrs wpeutil wpr wusa wuauclt wscript`)
	batAttributes = wordset(`on off disable enableextensions enabledelayedexpansion`)
)

func init() {
	registerRouge("batchfile", func() *rlexer {
		l := &rlexer{tag: "batchfile"}
		l.state("basic",
			rule(`(?i)@?\brem\b.*$`, "c"),
			rule(`^::.*$`, "c"),
			rule(`(?i):[a-z]+`, "nl"),
			ruleF(`(?i)([a-z]\w*)(\.exe|com|bat|cmd|msi)?`, func(c *rctx) {
				w := c.group(1)
				switch {
				case batDevices[w]:
					c.groups("kr", "err")
				case batKeywords[w]:
					c.groups("k", "err")
				case batOperatorWords[w]:
					c.groups("ow", "err")
				case batBuiltinCommands[w]:
					c.token("nb")
				case batOtherCommands[w]:
					c.token("nb")
				case batAttributes[w]:
					c.groups("na", "err")
				default:
					// Rouge は "set".casecmp(m[1]) を条件にしており、常に真になる
					c.groups("kd", "err")
				}
			}),
			rule(`(?i)((?:[\/\+]|--?)[a-z]+)\s*`, "na"),
			mixin("expansions"),
			rule(`[<>&|(){}\[\]\-+=;,~?*]`, "o"),
		)
		l.state("escape", rule(`(?m)\^.`, "se"))
		l.state("expansions",
			rule(`(?i)[%!]+([a-z_$@#]+)[%!]+`, "nv"),
			rule(`(?i)(\%+~?[a-z]+\d?)`, "vm"),
		)
		l.state("double_quotes",
			mixin("escape"),
			rule(`["]`, "s2", "#pop"),
			mixin("expansions"),
			rule(`[^\^"%!]+`, "s2"),
		)
		l.state("single_quotes",
			mixin("escape"),
			rule(`[']`, "s1", "#pop"),
			mixin("expansions"),
			rule(`[^\^'%!]+`, "s1"),
		)
		l.state("backtick",
			mixin("escape"),
			rule("[`]", "sb", "#pop"),
			mixin("expansions"),
			rule("[^\\^`%!]+", "sb"),
		)
		l.state("data",
			rule(`\s+`, ""),
			rule(`(?i)0x[0-9a-f]+`, "mh"),
			rule(`[0-9]`, "m"),
			rule(`["]`, "s2", "double_quotes"),
			rule(`[']`, "s1", "single_quotes"),
			rule("[`]", "sb", "backtick"),
			rule("[^\\s&|()\\[\\]{}\\^=;!%+\\-,\"'`~?*]+", ""),
			mixin("escape"),
		)
		l.state("root",
			mixin("basic"),
			mixin("data"),
		)
		return l
	})
}
