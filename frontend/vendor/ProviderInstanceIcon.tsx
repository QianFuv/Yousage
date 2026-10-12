/** The six history harness marks with native account initials and the original provider glyphs. */
import {ClaudeAI,OpenAI,CursorIcon,AntigravityIcon,type Icon} from "../../tools/parity/upstream/apps/web/src/components/Icons";
import {GrokIcon,OpenCodeIcon} from "./providerIcons";
import {providerInstanceInitials} from "../../tools/parity/src/bridges/instance";
import {cn} from "../../tools/parity/src/bridges/utils";
const MARKS:Record<string,Icon>={codex:OpenAI,claudeAgent:ClaudeAI,cursor:CursorIcon,grok:GrokIcon,opencode:OpenCodeIcon,antigravity:AntigravityIcon};
/** Render the account/harness mark without requesting an unrelated agent registry. */
export function ProviderInstanceIcon({driverKind,displayName,accentColor,showBadge=false,indicatorBackground="var(--card)",className,iconClassName}:{driverKind:string;displayName:string;accentColor?:string;showBadge?:boolean;indicatorBackground?:string;className?:string;iconClassName?:string}){
	const Mark=MARKS[driverKind];
	return <span className={cn("relative isolate z-30 inline-flex shrink-0 items-center justify-center overflow-visible",className)} data-provider-accent-color={accentColor}>
		{Mark?<Mark className={cn("size-5 shrink-0",iconClassName)} aria-hidden/>:<span className={cn("text-3xs font-semibold leading-none",iconClassName)}>{providerInstanceInitials(displayName)}</span>}
		{showBadge && <span className="absolute -bottom-1 -right-1 rounded-full px-0.5 text-3xs font-semibold" style={{background:accentColor??indicatorBackground,boxShadow:`0 0 0 2px ${indicatorBackground}`}}>{providerInstanceInitials(displayName)}</span>}
	</span>;
}
