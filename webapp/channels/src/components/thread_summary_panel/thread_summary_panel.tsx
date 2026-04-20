// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {memo, useCallback, useMemo, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';

import {
    AiSummarizeIcon,
    ArrowLeftIcon,
    CloseIcon,
    ChevronRightIcon,
    ChevronDownIcon,
} from '@mattermost/compass-icons/components';

import {closeRightHandSide} from 'actions/views/rhs';
import {backToThreadFromSummary, fetchThreadSummary} from 'actions/views/thread_summary';
import type {ThreadSummaryData} from 'reducers/views/thread_summary';
import {
    getThreadSummaryLoading,
    getThreadSummaryData,
    getThreadSummaryError,
    getThreadSummaryPostId,
} from 'selectors/views/thread_summary';

import AtMention from 'components/at_mention';

import './thread_summary_panel.scss';

function renderTextWithMentions(text: string): React.ReactNode {
    const parts = text.split(/(@\w[\w.-]*)/g);
    return parts.map((part, i) => {
        if (part.startsWith('@')) {
            return (
                <AtMention
                    key={i}
                    mentionName={part.slice(1)}
                    fetchMissingUsers={true}
                />
            );
        }
        return part;
    });
}

const MentionText = memo(({text}: {text: string}) => {
    const rendered = useMemo(() => renderTextWithMentions(text), [text]);
    return <>{rendered}</>;
});
MentionText.displayName = 'MentionText';

const ThreadSummaryPanel: React.FC = () => {
    const dispatch = useDispatch();
    const loading = useSelector(getThreadSummaryLoading);
    const data = useSelector(getThreadSummaryData);
    const error = useSelector(getThreadSummaryError);
    const postId = useSelector(getThreadSummaryPostId);
    const [detailsExpanded, setDetailsExpanded] = useState(false);

    const handleBack = useCallback(() => {
        dispatch(backToThreadFromSummary());
    }, [dispatch]);

    const handleClose = useCallback(() => {
        dispatch(closeRightHandSide());
    }, [dispatch]);

    const handleRetry = useCallback(() => {
        if (postId) {
            dispatch(fetchThreadSummary(postId));
        }
    }, [dispatch, postId]);

    const toggleDetails = useCallback(() => {
        setDetailsExpanded((prev) => !prev);
    }, []);

    return (
        <div className='ThreadSummaryPanel'>
            <div className='ThreadSummaryPanel__header'>
                <button
                    className='style--none back-button'
                    onClick={handleBack}
                >
                    <ArrowLeftIcon size={20}/>
                </button>
                <span className='title'>
                    <AiSummarizeIcon size={20}/>
                    <FormattedMessage
                        id='thread_summary.title'
                        defaultMessage='AI Summary'
                    />
                </span>
                <button
                    className='style--none close-button'
                    onClick={handleClose}
                >
                    <CloseIcon size={20}/>
                </button>
            </div>

            <div className='ThreadSummaryPanel__content'>
                {loading && (
                    <div className='ThreadSummaryPanel__loading'>
                        <AiSummarizeIcon size={24}/>
                        <FormattedMessage
                            id='thread_summary.loading'
                            defaultMessage='Generating summary...'
                        />
                    </div>
                )}

                {error && !loading && (
                    <div className='ThreadSummaryPanel__error'>
                        <p>{error}</p>
                        <button
                            className='style--none retry-button'
                            onClick={handleRetry}
                        >
                            <FormattedMessage
                                id='thread_summary.retry'
                                defaultMessage='Try again'
                            />
                        </button>
                    </div>
                )}

                {data && !loading && (
                    <>
                        <div className='ThreadSummaryPanel__channel-info'>
                            <FormattedMessage
                                id='thread_summary.post_count'
                                defaultMessage='{count} messages • {participants} participants'
                                values={{
                                    count: data.thread_post_count,
                                    participants: data.participants.length,
                                }}
                            />
                        </div>

                        <div className='ThreadSummaryPanel__summary'>
                            <MentionText text={data.summary}/>
                        </div>

                        {data.key_points.length > 0 && (
                            <>
                                <button
                                    className='style--none ThreadSummaryPanel__details-toggle'
                                    onClick={toggleDetails}
                                >
                                    {detailsExpanded ? (
                                        <ChevronDownIcon size={16}/>
                                    ) : (
                                        <ChevronRightIcon size={16}/>
                                    )}
                                    {detailsExpanded ? (
                                        <FormattedMessage
                                            id='thread_summary.less_detail'
                                            defaultMessage='Less detail'
                                        />
                                    ) : (
                                        <FormattedMessage
                                            id='thread_summary.more_detail'
                                            defaultMessage='More detail'
                                        />
                                    )}
                                </button>

                                {detailsExpanded && (
                                    <ul className='ThreadSummaryPanel__key-points'>
                                        {data.key_points.map((point: ThreadSummaryData['key_points'][number], idx: number) => (
                                            <li key={idx}><MentionText text={point.text}/></li>
                                        ))}
                                    </ul>
                                )}
                            </>
                        )}
                    </>
                )}
            </div>

            <div className='ThreadSummaryPanel__footer'>
                <FormattedMessage
                    id='thread_summary.disclaimer'
                    defaultMessage='AI-generated summary. May be inaccurate.'
                />
                <div className='feedback-buttons'>
                    <button type='button'>{'👍'}</button>
                    <button type='button'>{'👎'}</button>
                </div>
            </div>
        </div>
    );
};

export default memo(ThreadSummaryPanel);
