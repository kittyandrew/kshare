---
source: https://stackoverflow.com/questions/2031163/when-to-use-the-different-log-levels/2031209#2031209
updated: May 14, 2026
custom-line-limit: 150
---

# logging - When to use the different log levels - Stack Overflow
<!-- .claude/rules/00-project/033-log-levels.md: external reference, refresh by re-fetching from source URL -->

Quite broad question. So more than one answer is possible, depending on the actual circumstances of logging. Someone will miss `notice` in this collection someone will not ...

## Answer
I find it more helpful to think about severities from the perspective of viewing the log file.

**Fatal/Critical**: Overall application or system failure that should be investigated immediately. Yes, wake up the SysAdmin. Since we prefer our SysAdmins alert and well-rested, this severity should be used very infrequently. If it's happening daily and that's not a BFD, it has lost its meaning. Typically, a Fatal error only occurs once in the process lifetime, so if the log file is tied to the process, this is typically the last message in the log.

**Error**: Definitely a problem that should be investigated. SysAdmin should be notified automatically, but doesn't need to be dragged out of bed. By filtering a log to look at errors and above you get an overview of error frequency and can quickly identify the initiating failure that might have resulted in a cascade of additional errors. Tracking error rates as versus application usage can yield useful quality metrics such as MTBF which can be used to assess overall quality. For example, this metric might help inform decisions about whether or not another beta testing cycle is needed before a release.

**Warning**: This MIGHT be problem, or might not. For example, expected transient environmental conditions such as short loss of network or database connectivity should be logged as Warnings, not Errors. Viewing a log filtered to show only warnings and errors may give quick insight into early hints at the root cause of a subsequent error. Warnings should be used sparingly so that they don't become meaningless. For example, loss of network access should be a warning or even an error in a server application, but might be just an Info in a desktop app designed for occasionally disconnected laptop users.

**Info**: This is important information that should be logged under normal conditions such as successful initialization, services starting and stopping or successful completion of significant transactions. Viewing a log showing Info and above should give a quick overview of major state changes in the process providing top-level context for understanding any warnings or errors that also occur. Don't have too many Info messages. We typically have < 5% Info messages relative to Trace.

**Trace**: Trace is by far the most commonly used severity and should provide context to understand the steps leading up to errors and warnings. Having the right density of Trace messages makes software much more maintainable but requires some diligence because the value of individual Trace statements may change over time as programs evolve. The best way to achieve this is by getting the dev team in the habit of regularly reviewing logs as a standard part of troubleshooting customer reported issues. Encourage the team to prune out Trace messages that no longer provide useful context and to add messages where needed to understand the context of subsequent messages. For example, it is often helpful to log user input such as changing displays or tabs.

**Debug**: We consider Debug < Trace. The distinction being that Debug messages are compiled out of Release builds. That said, we discourage use of Debug messages. Allowing Debug messages tends to lead to more and more Debug messages being added and none ever removed. In time, this makes log files almost useless because it's too hard to filter signal from noise. That causes devs to not use the logs which continues the death spiral. In contrast, constantly pruning Trace messages encourages devs to use them which results in a virtuous spiral. Also, this eliminates the possibility of bugs introduced because of needed side-effects in debug code that isn't included in the release build. Yeah, I know that shouldn't happen in good code, but better safe than sorry.
