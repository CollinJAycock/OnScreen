' Put a card's watch indicator and optional second line on its
' ContentNode, in the fields PosterCard reads (watchBadge, unwatchedCount,
' progressPct, cardSubtitle). The badge comes from WatchState_CardBadge,
' so the including component also needs source/util/WatchState.brs.
' Call on a freshly created node.
sub CardFields_Apply(node as Object, item as Object, subtitle as String)
    badge = WatchState_CardBadge(item)
    node.addFields({
        cardSubtitle: subtitle
        watchBadge: badge.kind
        unwatchedCount: badge.count
        progressPct: badge.pct
    })
end sub
