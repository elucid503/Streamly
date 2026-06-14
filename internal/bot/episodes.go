package bot

import (
	"context"
	"fmt"
	"sort"

	"streamly/internal/febapi"
	"streamly/internal/pool"
)

type nextEpisode struct {

	FID int

	Season int
	Episode int

	FileName string

}

func (b *Bot) resolveNextEpisode(ctx context.Context, auto *pool.AutoNextContext) (*nextEpisode, error) {

	if auto == nil {

		return nil, fmt.Errorf("no auto-next context")

	}

	current, err := b.findEpisodeInSeason(ctx, auto.ShareKey, auto.Season, auto.Episode)

	if err != nil {

		return nil, err

	}

	if current != nil {

		return current, nil

	}

	return b.findFirstEpisodeInNextSeason(ctx, auto.ShareKey, auto.Season)

}

func (b *Bot) findEpisodeInSeason(ctx context.Context, shareKey string, season, episode int) (*nextEpisode, error) {

	_ = ctx

	root, err := b.Resolver.ListChildren(shareKey, 0)

	if err != nil {

		return nil, err

	}

	seasons := b.Resolver.Seasons(root)

	if len(seasons) == 0 {

		return b.nextFromFlatListing(root, episode)

	}

	seasonFID, seasonNumber := seasonFolder(seasons, season)

	if seasonFID == 0 {

		return nil, nil

	}

	children, err := b.Resolver.ListChildren(shareKey, seasonFID)

	if err != nil {

		return nil, err

	}

	episodes := toEpisodes(b.Resolver.Files(children))

	for _, ep := range episodes {

		if ep.Number == episode+1 {

			return &nextEpisode{

				FID: ep.FID,

				Season: seasonNumber,
				Episode: ep.Number,

				FileName: ep.FileName,

			}, nil

		}

	}

	return nil, nil

}

func (b *Bot) findFirstEpisodeInNextSeason(ctx context.Context, shareKey string, season int) (*nextEpisode, error) {

	_ = ctx

	root, err := b.Resolver.ListChildren(shareKey, 0)

	if err != nil {

		return nil, err

	}

	seasons := b.Resolver.Seasons(root)

	if len(seasons) == 0 {

		return nil, nil

	}

	type seasonEntry struct {

		FID int
		Number int

	}

	entries := make([]seasonEntry, 0, len(seasons))

	for index, folder := range seasons {

		info := seasonInfo(folder.FileName, index+1)
		entries = append(entries, seasonEntry{FID: folder.FID, Number: info.Number})

	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Number < entries[j].Number })

	for _, entry := range entries {

		if entry.Number <= season {

			continue

		}

		children, err := b.Resolver.ListChildren(shareKey, entry.FID)

		if err != nil {

			return nil, err

		}

		episodes := toEpisodes(b.Resolver.Files(children))

		if len(episodes) == 0 {

			continue

		}

		first := episodes[0]

		return &nextEpisode{

			FID: first.FID,

			Season: entry.Number,
			Episode: first.Number,

			FileName: first.FileName,

		}, nil

	}

	return nil, nil

}

func (b *Bot) nextFromFlatListing(root []febapi.FebboxFile, episode int) (*nextEpisode, error) {

	episodes := toEpisodes(b.Resolver.Files(root))

	for _, ep := range episodes {

		if ep.Number == episode + 1 {

			return &nextEpisode{

				FID: ep.FID,

				Season: 1,
				Episode: ep.Number,

				FileName: ep.FileName,

			}, nil

		}

	}

	return nil, nil

}

func seasonFolder(seasons []febapi.FebboxFile, season int) (fid int, number int) {

	for index, folder := range seasons {

		info := seasonInfo(folder.FileName, index+1)

		if info.Number == season {

			return folder.FID, info.Number

		}

	}

	return 0, 0

}
