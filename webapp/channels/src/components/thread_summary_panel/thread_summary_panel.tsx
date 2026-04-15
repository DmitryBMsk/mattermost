// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {memo, useCallback, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';

import {
    AiSummarizeIcon,
    ArrowLeftIcon,
    CloseIcon,
    ChevronRightIcon,
    ChevronDownIcon,
} from '@mattermost/compass-icons/components';

import {closeRightHandSide, goBack} from 'actions/views/rhs';
import {fetchThreadSummary} from 'actions/views/thread_summary';
import {
    getThreadSummaryLoading,
    getThreadSummaryData,
    getThreadSummaryError,
    getThreadSummaryPostId,
} from 'selectors/views/thread_summary';

import './thread_summary_panel.scss';

const ThreadSummaryPanel: React.FC = () => {
    const dispatch = useDispatch();
    const loading = useSelector(getThreadSummaryLoading);
    const data = useSelector(getThreadSummaryData);
    const error = useSelector(getThreadSummaryError);
    const postId = useSelector(getThreadSummaryPostId);
    const [detailsExpanded, setDetailsExpanded] = useState(false);

    const handleBack = useCallback(() => {
        dispatch(goBack());
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
                <span
                    className='back-button'
                    onClick={handleBack}
                    role='button'
                    tabIndex={0}
                >
                    <ArrowLeftIcon size={20}/>
                </span>
                <span className='title'>
                    <AiSummarizeIcon size={20}/>
                    <FormattedMessage
                        id='thread_summary.title'
                        defaultMessage='AI Summary'
                    />
                </span>
                <span
                    className='close-button'
                    onClick={handleClose}
                    role='button'
                    tabIndex={0}
                >
                    <CloseIcon size={20}/>
                </span>
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
                        <span
                            className='retry-button'
                            onClick={handleRetry}
                            role='button'
                            tabIndex={0}
                        >
                            <FormattedMessage
                                id='thread_summary.retry'
                                defaultMessage='Try again'
                            />
                        </span>
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
                            {data.summary}
                        </div>

                        {data.key_points.length > 0 && (
                            <>
                                <div
                                    className='ThreadSummaryPanel__details-toggle'
                                    onClick={toggleDetails}
                                    role='button'
                                    tabIndex={0}
                                >
                                    {detailsExpanded ? (
                                        <ChevronDownIcon size={16}/>
                                    ) : (
                                        <ChevronRightIcon size={16}/>
                                    )}
                                    <FormattedMessage
                                        id={detailsExpanded ? 'thread_summary.less_detail' : 'thread_summary.more_detail'}
                                        defaultMessage={detailsExpanded ? 'Less detail' : 'More detail'}
                                    />
                                </div>

                                {detailsExpanded && (
                                    <ul className='ThreadSummaryPanel__key-points'>
                                        {data.key_points.map((point, idx) => (
                                            <li key={idx}>{point.text}</li>
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
